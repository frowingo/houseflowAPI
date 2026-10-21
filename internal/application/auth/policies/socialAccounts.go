package policies

import (
	"context"
	"errors"
	"strings"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const maxSocialNameLength = 50

var errSocialSignUpRace = errors.New("social sign-up raced with another request")

// SocialAccounts owns signing in with a verified provider identity and
// creating the account behind it. Social sign-in and the completion of an
// email-less social sign-up share it.
type SocialAccounts struct {
	userRepository        databaseAbstract.DbRepository[entities.User]
	identityRepository    databaseAbstract.DbRepository[entities.UserIdentity]
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory]
	jwtService            *helpers.JWTService
}

func NewSocialAccounts(
	userRepository databaseAbstract.DbRepository[entities.User],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory],
	jwtService *helpers.JWTService,
) *SocialAccounts {
	return &SocialAccounts{
		userRepository:        userRepository,
		identityRepository:    identityRepository,
		userHistoryRepository: userHistoryRepository,
		jwtService:            jwtService,
	}
}

// NewSocialAccount describes the account to create for a provider identity.
type NewSocialAccount struct {
	Identity      authAbstract.VerifiedIdentity
	Email         string
	EmailVerified bool
	Firstname     string
	Lastname      string
	Language      string
	// KeptToken is what a SocialAuthorizationKeeper kept (Apple), if any.
	KeptToken string
}

// SignIn issues a token for the user the provider account is linked to.
// signedIn is false when the provider account is not linked to anyone.
func (a *SocialAccounts) SignIn(ctx context.Context, identity authAbstract.VerifiedIdentity) (dtos.SocialAuthResponseModel, bool, error) {
	linked, err := a.identityRepository.FindOneByFilter(ctx, bson.M{
		"provider": identity.Provider, "providerUserId": identity.ProviderUserID,
	})
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		return dtos.SocialAuthResponseModel{}, false, nil
	}
	if err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}

	userID, err := helpers.ToMongoId(linked.UserId)
	if err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}
	user, err := a.userRepository.FindByID(ctx, userID)
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		// The user is gone but the link was left behind; drop it and sign up again.
		if err := a.identityRepository.Delete(ctx, linked.Id); err != nil &&
			!helpers.IsApplicationError(err, "database.error.delete_not_found") {
			return dtos.SocialAuthResponseModel{}, false, err
		}
		return dtos.SocialAuthResponseModel{}, false, nil
	}
	if err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}
	if !user.IsActive {
		return dtos.SocialAuthResponseModel{}, false, helpers.NewLocalizedError("auth.error.account_locked")
	}

	now := time.Now()
	updated, err := a.userRepository.Collection().UpdateOne(ctx,
		bson.M{"_id": user.Id, "isActive": true},
		bson.M{"$set": bson.M{"lastLogin": now}},
	)
	if err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}
	if updated.MatchedCount == 0 {
		return dtos.SocialAuthResponseModel{}, false, helpers.NewLocalizedError("auth.error.account_locked")
	}
	if err := a.identityRepository.UpdateFields(ctx, linked.Id, bson.M{"lastLoginOn": now}); err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}

	token, err := a.jwtService.GenerateToken(user.Email, user.Id.Hex(), int(user.Role), user.Language)
	return dtos.SocialAuthResponseModel{Token: token}, true, err
}

// EnsureEmailAvailable rejects an email that already belongs to an account.
// Accounts are never merged automatically: an unverified account created with
// someone else's address would otherwise gain their provider login. Older
// accounts may keep their original casing, so the check ignores case; it
// cannot use the email index, which is acceptable for a sign-up.
func (a *SocialAccounts) EnsureEmailAvailable(ctx context.Context, email string) error {
	existing, err := a.userRepository.Collection().CountDocuments(ctx, bson.M{"email": helpers.NormalizeEmail(email)},
		options.Count().SetCollation(&options.Collation{Locale: "en", Strength: 2}).SetLimit(1))
	if err != nil {
		return err
	}
	if existing > 0 {
		return helpers.NewConflictError("auth.error.email_registered_with_other_method")
	}
	return nil
}

// Create writes the user, the provider link and the profile history in one
// transaction. When another request wins the race for the same provider
// account, that account is signed in instead.
func (a *SocialAccounts) Create(ctx context.Context, account NewSocialAccount) (dtos.SocialAuthResponseModel, error) {
	identity := account.Identity
	now := time.Now()
	newUser := entities.User{
		Email:     helpers.NormalizeEmail(account.Email),
		Firstname: SocialName(account.Firstname, identity.FirstName),
		Lastname:  SocialName(account.Lastname, identity.LastName),
		Language:  helpers.NormalizeLanguage(account.Language),
		CreatedOn: now, UpdatedOn: now, LastLogin: now,
		IsActive: true, IsVerifyEmail: account.EmailVerified,
		HouseIds: []string{}, Role: entities.Normal,
		SignUpProvider: identity.Provider,
	}

	var createdUser *entities.User
	err := a.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		var err error
		createdUser, err = a.userRepository.Insert(txCtx, newUser)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errSocialSignUpRace
			}
			return err
		}
		if _, err := a.identityRepository.Insert(txCtx, entities.UserIdentity{
			UserId:               createdUser.Id.Hex(),
			Provider:             identity.Provider,
			ProviderUserId:       identity.ProviderUserID,
			ProviderEmail:        identity.Email,
			EmailVerified:        identity.EmailVerified,
			IsPrivateRelay:       identity.IsPrivateRelay,
			ProviderClientId:     identity.ClientID,
			AppleRefreshTokenEnc: account.KeptToken,
			CreatedOn:            now,
			LastLoginOn:          now,
		}); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errSocialSignUpRace
			}
			return err
		}
		return a.userHistoryRepository.InsertMany(txCtx, []entities.UserInfoHistory{
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnFirstName, Value: createdUser.Firstname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnLastName, Value: createdUser.Lastname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnRole, Value: int(createdUser.Role), UpdateOn: now},
		})
	})
	if errors.Is(err, errSocialSignUpRace) {
		// Another request created the account or took the email first.
		result, signedIn, signInErr := a.SignIn(ctx, identity)
		if signedIn || signInErr != nil {
			return result, signInErr
		}
		return dtos.SocialAuthResponseModel{}, helpers.NewConflictError("auth.error.email_registered_with_other_method")
	}
	if err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}

	token, err := a.jwtService.GenerateToken(createdUser.Email, createdUser.Id.Hex(), int(createdUser.Role), createdUser.Language)
	return dtos.SocialAuthResponseModel{Token: token, IsNewUser: true}, err
}

// SocialName prefers the name the client sent and falls back to the one the
// provider shared, trimmed to the profile limit.
func SocialName(requested, fromProvider string) string {
	name := strings.TrimSpace(requested)
	if name == "" {
		name = strings.TrimSpace(fromProvider)
	}
	if runes := []rune(name); len(runes) > maxSocialNameLength {
		name = strings.TrimSpace(string(runes[:maxSocialNameLength]))
	}
	return name
}
