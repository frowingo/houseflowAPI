package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

type AuthService struct {
	dbRepository        *abstract.DbRepository[entities.User]
	notificationService *NotificationService
}

func NewAuthService(dbRepository *abstract.DbRepository[entities.User], notificationService *NotificationService) *AuthService {
	return &AuthService{
		dbRepository:        dbRepository,
		notificationService: notificationService,
	}
}

func (r *AuthService) GetUserByID(userId string) (*entities.User, error) {
	objectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return nil, err
	}

	return r.dbRepository.FindById(objectId)
}

func (r *AuthService) Login(email string, password string) (string, error) {
	const MaxFailedAttempts = 10

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil && err.Error() == "database.error.document_not_found" {
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
		// Password correct: reset failed attempts and update last login
		token, err := helpers.GenerateToken(user.Email, user.Id.Hex(), int(user.Role), user.Language)
		if err != nil {
			return "", err
		}

		_ = r.dbRepository.UpdateFields(user.Id, bson.M{
			"lastLogin":           time.Now(),
			"failedLoginAttempts": 0,
		})

		return token, nil
	}

	// Password incorrect: increment failed attempts
	user.FailedLoginAttempts++
	updateData := bson.M{"failedLoginAttempts": user.FailedLoginAttempts}

	// Lock account if max attempts reached
	if user.FailedLoginAttempts >= MaxFailedAttempts {
		updateData["isActive"] = false
		_ = r.dbRepository.UpdateFields(user.Id, updateData)
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}

	_ = r.dbRepository.UpdateFields(user.Id, updateData)
	return "", helpers.NewLocalizedError("auth.error.invalid_password")
}

func (r *AuthService) SignUp(model dtos.SignUpUserModel) (string, error) {

	user, err := r.dbRepository.FindByColumn("email", model.Email)

	// user email must unique
	if user != nil {
		return "", helpers.NewLocalizedError("auth.error.user_already_exists")
	} else {
		if err != nil && err.Error() != "database.error.document_not_found" {
			return "", err
		}
	}

	hashedPassword, err := helpers.HashPassword(model.Password)
	if err != nil {
		return "", err
	}

	model.Password = hashedPassword
	entity := model.ToEntity()

	newUser, err := r.dbRepository.Insert(entity)
	if err != nil {
		return "", err
	}

	token, err := helpers.GenerateToken(newUser.Email, newUser.Id.Hex(), int(newUser.Role), newUser.Language)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (r *AuthService) ForgotPassword(email string) error {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil {

		if user == nil || err.Error() == "database.error.document_not_found" {
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

func (r *AuthService) ResetPassword(email, code, newPassword string) error {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	if !helpers.IsResetCodeValid(email, code, cfg.Internal.PasswordReset.Secret, cfg.Internal.PasswordReset.ValidityMinutes) {
		return helpers.NewLocalizedError("auth.error.invalid_or_expired_reset_code")
	}

	user, err := r.dbRepository.FindByColumn("email", email)
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

	return r.dbRepository.UpdateFields(user.Id, bson.M{
		"password":            hashedPassword,
		"isActive":            true,
		"failedLoginAttempts": 0,
		"updatedOn":           time.Now(),
	})
}

func (r *AuthService) SendEmailVerificationCode(email string) error {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	user, err := r.dbRepository.FindByColumn("email", email)
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

func (r *AuthService) ValidateEmail(email, code string) error {

	cfg, err := config.MustLoadConfig()
	if err != nil {
		return err
	}

	if !helpers.IsResetCodeValid(email, code, cfg.Internal.PasswordReset.Secret, cfg.Internal.PasswordReset.ValidityMinutes) {
		return helpers.NewLocalizedError("auth.error.invalid_or_expired_email_verification_code")
	}

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil {
		return err
	}
	if user == nil {
		return helpers.NewLocalizedError("user.error.not_found")
	}
	if user.IsVerifyEmail {
		return nil
	}

	return r.dbRepository.UpdateFields(user.Id, bson.M{
		"isVerifyEmail": true,
		"updatedOn":     time.Now(),
	})
}
