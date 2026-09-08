package services

import (
	"context"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type AuthService struct {
	dbRepository              *abstract.DbRepository[entities.User]
	userInfoHistoryRepository *abstract.DbRepository[entities.UserInfoHistory]
	notificationService       *NotificationService
}

func NewAuthService(
	dbRepository *abstract.DbRepository[entities.User],
	notificationService *NotificationService,
	userInfoHistoryRepositories ...*abstract.DbRepository[entities.UserInfoHistory],
) *AuthService {
	var userInfoHistoryRepository *abstract.DbRepository[entities.UserInfoHistory]
	if len(userInfoHistoryRepositories) > 0 {
		userInfoHistoryRepository = userInfoHistoryRepositories[0]
	}

	return &AuthService{
		dbRepository:              dbRepository,
		userInfoHistoryRepository: userInfoHistoryRepository,
		notificationService:       notificationService,
	}
}

func (r *AuthService) GetUserByID(ctx context.Context, userId string) (*entities.User, error) {
	objectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return nil, err
	}

	return r.dbRepository.FindByID(ctx, objectId)
}

func (r *AuthService) Login(ctx context.Context, email string, password string) (string, error) {
	const MaxFailedAttempts = 10

	user, err := r.dbRepository.FindByColumn(ctx, "email", email)
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		return "", helpers.NewLocalizedError("auth.error.email_not_found")
	} else if err != nil {
		return "", err
	}

	// Check if account is locked
	if !user.IsActive {
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}

	isValid := helpers.CheckPasswordHash(password, user.HashPassword)
	if isValid {
		result, err := r.dbRepository.Collection().UpdateOne(ctx, bson.M{
			"_id": user.Id, "isActive": true,
		}, bson.M{"$set": bson.M{
			"lastLogin": time.Now(), "failedLoginAttempts": 0,
		}})
		if err != nil {
			return "", err
		}
		if result.MatchedCount == 0 {
			return "", helpers.NewLocalizedError("auth.error.account_locked")
		}
		token, err := helpers.GenerateToken(user.Email, user.Id.Hex(), int(user.Role), user.Language)
		if err != nil {
			return "", err
		}
		return token, nil
	}

	var updated entities.User
	err = r.dbRepository.Collection().FindOneAndUpdate(ctx,
		bson.M{"_id": user.Id, "isActive": true},
		mongo.Pipeline{
			{{Key: "$set", Value: bson.M{
				"failedLoginAttempts": bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$failedLoginAttempts", 0}}, 1}},
			}}},
			{{Key: "$set", Value: bson.M{
				"isActive": bson.M{"$cond": bson.A{
					bson.M{"$gte": bson.A{"$failedLoginAttempts", MaxFailedAttempts}}, false, "$isActive",
				}},
			}}},
		}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err == mongo.ErrNoDocuments {
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}
	if err != nil {
		return "", err
	}
	if !updated.IsActive {
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}
	return "", helpers.NewLocalizedError("auth.error.invalid_password")
}

func (r *AuthService) SignUp(ctx context.Context, model dtos.SignUpUserModel) (string, error) {

	user, err := r.dbRepository.FindByColumn(ctx, "email", model.Email)

	// user email must unique
	if user != nil {
		return "", helpers.NewConflictError("auth.error.user_already_exists")
	} else {
		if err != nil && !helpers.IsApplicationError(err, "database.error.document_not_found") {
			return "", err
		}
	}

	hashedPassword, err := helpers.HashPassword(model.Password)
	if err != nil {
		return "", err
	}

	model.Password = hashedPassword
	entity := model.ToEntity()

	var newUser *entities.User
	err = r.dbRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		newUser, err = r.dbRepository.Insert(txCtx, entity)
		if err != nil {
			return err
		}
		return insertUserInfoHistory(txCtx, r.userInfoHistoryRepository,
			newUserInfoHistoryEntries(*newUser, newUser.CreatedOn))
	})
	if err != nil {
		return "", err
	}

	token, err := helpers.GenerateToken(newUser.Email, newUser.Id.Hex(), int(newUser.Role), newUser.Language)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (r *AuthService) ForgotPassword(ctx context.Context, email string) error {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	user, err := r.dbRepository.FindByColumn(ctx, "email", email)
	if err != nil {

		if user == nil || helpers.IsApplicationError(err, "database.error.document_not_found") {
			return helpers.NewLocalizedError("user.error.not_found")
		}

		return err
	}

	window := helpers.ResetCodeWindow(cfg.Internal.PasswordReset.ValidityMinutes)
	code := helpers.GenerateResetCode(email, cfg.Internal.PasswordReset.Secret, window)

	if err := r.notificationService.SendResetCodeEmail(email, code, cfg.Internal.PasswordReset.ValidityMinutes); err != nil {
		return err
	}

	return nil
}

func (r *AuthService) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	if !helpers.IsResetCodeValid(email, code, cfg.Internal.PasswordReset.Secret, cfg.Internal.PasswordReset.ValidityMinutes) {
		return helpers.NewLocalizedError("auth.error.invalid_or_expired_reset_code")
	}

	user, err := r.dbRepository.FindByColumn(ctx, "email", email)
	if err != nil {
		return err
	}
	if user == nil {
		return helpers.NewLocalizedError("user.error.not_found")
	}

	hashedPassword, err := helpers.HashPassword(newPassword)
	if err != nil {
		return err
	}

	return r.dbRepository.UpdateFields(ctx, user.Id, bson.M{
		"password":            hashedPassword,
		"isActive":            true,
		"failedLoginAttempts": 0,
		"updatedOn":           time.Now(),
	})
}

func (r *AuthService) SendEmailVerificationCode(ctx context.Context, email string) error {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	user, err := r.dbRepository.FindByColumn(ctx, "email", email)
	if err != nil {
		return err
	}
	if user == nil {
		return helpers.NewLocalizedError("user.error.not_found")
	}
	if user.IsVerifyEmail {
		return nil
	}

	window := helpers.ResetCodeWindow(cfg.Internal.PasswordReset.ValidityMinutes)
	code := helpers.GenerateResetCode(email, cfg.Internal.PasswordReset.Secret, window)

	return r.notificationService.SendEmailVerificationCode(email, code, cfg.Internal.PasswordReset.ValidityMinutes)
}

func (r *AuthService) ValidateEmail(ctx context.Context, email, code string) error {

	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	if !helpers.IsResetCodeValid(email, code, cfg.Internal.PasswordReset.Secret, cfg.Internal.PasswordReset.ValidityMinutes) {
		return helpers.NewLocalizedError("auth.error.invalid_or_expired_email_verification_code")
	}

	user, err := r.dbRepository.FindByColumn(ctx, "email", email)
	if err != nil {
		return err
	}
	if user == nil {
		return helpers.NewLocalizedError("user.error.not_found")
	}
	if user.IsVerifyEmail {
		return nil
	}

	return r.dbRepository.UpdateFields(ctx, user.Id, bson.M{
		"isVerifyEmail": true,
		"updatedOn":     time.Now(),
	})
}
