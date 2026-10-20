package commands

import (
	"context"
	"errors"
	"strings"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const maxSocialNameLength = 50

var errSocialSignUpRace = errors.New("social sign-up raced with another request")

// SocialAuthCommand signs a user in with a provider credential, creating the
// account on first use. The provider only proves the identity; the session is
// our own JWT.
type SocialAuthCommand struct {
	cqrs.Request[dtos.SocialAuthResponseModel]
	Provider   entities.AuthProvider
	Credential authAbstract.SocialCredential
	// Firstname and Lastname are used only when the account is created. Apple
	// gives the name to the client on the first authorization only.
	Firstname string
	Lastname  string
	Language  string
}

type SocialAuthHandler struct {
	userRepository        databaseAbstract.DbRepository[entities.User]
	identityRepository    databaseAbstract.DbRepository[entities.UserIdentity]
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory]
	jwtService            *helpers.JWTService
	verifiers             map[entities.AuthProvider]authAbstract.SocialIdentityVerifier
	keepers               map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper
}

// NewSocialAuthHandler takes the verifiers of the enabled providers; a
// provider without a verifier is reported as not supported.
func NewSocialAuthHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory],
	jwtService *helpers.JWTService,
	verifiers []authAbstract.SocialIdentityVerifier,
	keepers []authAbstract.SocialAuthorizationKeeper,
) *SocialAuthHandler {
	handler := &SocialAuthHandler{
		userRepository:        userRepository,
		identityRepository:    identityRepository,
		userHistoryRepository: userHistoryRepository,
		jwtService:            jwtService,
		verifiers:             make(map[entities.AuthProvider]authAbstract.SocialIdentityVerifier, len(verifiers)),
		keepers:               make(map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper, len(keepers)),
	}
	for _, verifier := range verifiers {
		handler.verifiers[verifier.Provider()] = verifier
	}
	for _, keeper := range keepers {
		handler.keepers[keeper.Provider()] = keeper
	}
	return handler
}

func (h *SocialAuthHandler) Handle(ctx context.Context, command SocialAuthCommand) (dtos.SocialAuthResponseModel, error) {
	verifier, ok := h.verifiers[command.Provider]
	if !ok {
		return dtos.SocialAuthResponseModel{}, helpers.NewLocalizedError("auth.error.provider_not_supported")
	}
	identity, err := verifier.Verify(ctx, command.Credential)
	if err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}

	result, signedIn, err := h.signIn(ctx, identity)
	if signedIn || err != nil {
		return result, err
	}
	return h.signUp(ctx, command, identity)
}

// signIn issues a token for the user the provider account is linked to.
// signedIn is false when the provider account is not linked to anyone.
func (h *SocialAuthHandler) signIn(ctx context.Context, identity authAbstract.VerifiedIdentity) (dtos.SocialAuthResponseModel, bool, error) {
	linked, err := h.identityRepository.FindOneByFilter(ctx, bson.M{
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
	user, err := h.userRepository.FindByID(ctx, userID)
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		// The user is gone but the link was left behind; drop it and sign up again.
		if err := h.identityRepository.Delete(ctx, linked.Id); err != nil &&
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
	updated, err := h.userRepository.Collection().UpdateOne(ctx,
		bson.M{"_id": user.Id, "isActive": true},
		bson.M{"$set": bson.M{"lastLogin": now}},
	)
	if err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}
	if updated.MatchedCount == 0 {
		return dtos.SocialAuthResponseModel{}, false, helpers.NewLocalizedError("auth.error.account_locked")
	}
	if err := h.identityRepository.UpdateFields(ctx, linked.Id, bson.M{"lastLoginOn": now}); err != nil {
		return dtos.SocialAuthResponseModel{}, false, err
	}

	token, err := h.jwtService.GenerateToken(user.Email, user.Id.Hex(), int(user.Role), user.Language)
	return dtos.SocialAuthResponseModel{Token: token}, true, err
}

func (h *SocialAuthHandler) signUp(
	ctx context.Context,
	command SocialAuthCommand,
	identity authAbstract.VerifiedIdentity,
) (dtos.SocialAuthResponseModel, error) {
	email := helpers.NormalizeEmail(identity.Email)
	if email == "" {
		return dtos.SocialAuthResponseModel{}, helpers.NewLocalizedError("auth.error.social_email_required")
	}
	// Accounts are never merged automatically: an unverified account created
	// with someone else's address would otherwise gain their provider login.
	// Older accounts may keep their original casing, so the check ignores case.
	// It cannot use the email index, which is acceptable for a sign-up.
	existing, err := h.userRepository.Collection().CountDocuments(ctx, bson.M{"email": email},
		options.Count().SetCollation(&options.Collation{Locale: "en", Strength: 2}).SetLimit(1))
	if err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}
	if existing > 0 {
		return dtos.SocialAuthResponseModel{}, helpers.NewConflictError("auth.error.email_registered_with_other_method")
	}

	keptToken := ""
	if keeper, ok := h.keepers[identity.Provider]; ok {
		keptToken, err = keeper.Keep(ctx, identity, command.Credential)
		if err != nil {
			return dtos.SocialAuthResponseModel{}, err
		}
	}

	now := time.Now()
	newUser := entities.User{
		Email:     email,
		Firstname: socialName(command.Firstname, identity.FirstName),
		Lastname:  socialName(command.Lastname, identity.LastName),
		Language:  helpers.NormalizeLanguage(command.Language),
		CreatedOn: now, UpdatedOn: now, LastLogin: now,
		IsActive: true, IsVerifyEmail: identity.EmailVerified,
		HouseIds: []string{}, Role: entities.Normal,
		SignUpProvider: identity.Provider,
	}

	var createdUser *entities.User
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		var err error
		createdUser, err = h.userRepository.Insert(txCtx, newUser)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errSocialSignUpRace
			}
			return err
		}
		if _, err := h.identityRepository.Insert(txCtx, entities.UserIdentity{
			UserId:               createdUser.Id.Hex(),
			Provider:             identity.Provider,
			ProviderUserId:       identity.ProviderUserID,
			ProviderEmail:        identity.Email,
			EmailVerified:        identity.EmailVerified,
			IsPrivateRelay:       identity.IsPrivateRelay,
			ProviderClientId:     identity.ClientID,
			AppleRefreshTokenEnc: keptToken,
			CreatedOn:            now,
			LastLoginOn:          now,
		}); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errSocialSignUpRace
			}
			return err
		}
		return h.userHistoryRepository.InsertMany(txCtx, []entities.UserInfoHistory{
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnFirstName, Value: createdUser.Firstname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnLastName, Value: createdUser.Lastname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnRole, Value: int(createdUser.Role), UpdateOn: now},
		})
	})
	if errors.Is(err, errSocialSignUpRace) {
		// Another request created the account or took the email first.
		result, signedIn, signInErr := h.signIn(ctx, identity)
		if signedIn || signInErr != nil {
			return result, signInErr
		}
		return dtos.SocialAuthResponseModel{}, helpers.NewConflictError("auth.error.email_registered_with_other_method")
	}
	if err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}

	token, err := h.jwtService.GenerateToken(createdUser.Email, createdUser.Id.Hex(), int(createdUser.Role), createdUser.Language)
	return dtos.SocialAuthResponseModel{Token: token, IsNewUser: true}, err
}

// socialName prefers the name the client sent and falls back to the one the
// provider shared, trimmed to the profile limit.
func socialName(requested, fromProvider string) string {
	name := strings.TrimSpace(requested)
	if name == "" {
		name = strings.TrimSpace(fromProvider)
	}
	if runes := []rune(name); len(runes) > maxSocialNameLength {
		name = strings.TrimSpace(string(runes[:maxSocialNameLength]))
	}
	return name
}

var _ cqrs.CommandHandler[SocialAuthCommand, dtos.SocialAuthResponseModel] = (*SocialAuthHandler)(nil)
