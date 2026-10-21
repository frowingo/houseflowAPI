package commands

import (
	"context"
	"errors"
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

// linkedProviders lists the providers connected to the user, in provider order.
func linkedProviders(
	ctx context.Context,
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	userID string,
) (dtos.LinkedProvidersResponseModel, error) {
	identities, err := identityRepository.FindManyByFilter(ctx, bson.M{"userId": userID},
		options.Find().SetProjection(bson.M{"provider": 1}).SetSort(bson.D{{Key: "provider", Value: 1}}))
	if err != nil {
		return dtos.LinkedProvidersResponseModel{}, err
	}
	providers := make([]entities.AuthProvider, 0, len(identities))
	for _, identity := range identities {
		providers = append(providers, identity.Provider)
	}
	return dtos.LinkedProvidersResponseModel{LinkedProviders: providers}, nil
}

// LinkSocialProviderCommand connects a provider account to the signed-in
// user, so they can sign in with it too. This is how a user whose email is
// already registered adds a provider: accounts are never merged
// automatically (ADR-023).
type LinkSocialProviderCommand struct {
	cqrs.Request[dtos.LinkedProvidersResponseModel]
	UserID     string
	Provider   entities.AuthProvider
	Credential authAbstract.SocialCredential
}

type LinkSocialProviderHandler struct {
	userRepository     databaseAbstract.DbRepository[entities.User]
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity]
	verifiers          map[entities.AuthProvider]authAbstract.SocialIdentityVerifier
	keepers            map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper
}

func NewLinkSocialProviderHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	verifiers []authAbstract.SocialIdentityVerifier,
	keepers []authAbstract.SocialAuthorizationKeeper,
) *LinkSocialProviderHandler {
	handler := &LinkSocialProviderHandler{
		userRepository:     userRepository,
		identityRepository: identityRepository,
		verifiers:          make(map[entities.AuthProvider]authAbstract.SocialIdentityVerifier, len(verifiers)),
		keepers:            make(map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper, len(keepers)),
	}
	for _, verifier := range verifiers {
		handler.verifiers[verifier.Provider()] = verifier
	}
	for _, keeper := range keepers {
		handler.keepers[keeper.Provider()] = keeper
	}
	return handler
}

func (h *LinkSocialProviderHandler) Handle(ctx context.Context, command LinkSocialProviderCommand) (dtos.LinkedProvidersResponseModel, error) {
	verifier, ok := h.verifiers[command.Provider]
	if !ok {
		return dtos.LinkedProvidersResponseModel{}, helpers.NewLocalizedError("auth.error.provider_not_supported")
	}
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return dtos.LinkedProvidersResponseModel{}, helpers.NewLocalizedError("user.error.invalid_user_id")
	}
	user, err := h.userRepository.FindByID(ctx, userObjectID)
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		return dtos.LinkedProvidersResponseModel{}, helpers.NewNotFoundError("user.error.not_found")
	}
	if err != nil {
		return dtos.LinkedProvidersResponseModel{}, err
	}
	if !user.IsActive {
		return dtos.LinkedProvidersResponseModel{}, helpers.NewLocalizedError("auth.error.account_locked")
	}

	identity, err := verifier.Verify(ctx, command.Credential)
	if err != nil {
		return dtos.LinkedProvidersResponseModel{}, err
	}
	if err := h.ensureLinkable(ctx, command.UserID, identity); err != nil {
		if errors.Is(err, errAlreadyLinkedHere) {
			return linkedProviders(ctx, h.identityRepository, command.UserID)
		}
		return dtos.LinkedProvidersResponseModel{}, err
	}

	keptToken := ""
	if keeper, ok := h.keepers[identity.Provider]; ok {
		keptToken, err = keeper.Keep(ctx, identity, command.Credential)
		if err != nil {
			return dtos.LinkedProvidersResponseModel{}, err
		}
	}
	now := time.Now()
	if _, err := h.identityRepository.Insert(ctx, entities.UserIdentity{
		UserId:               command.UserID,
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
		if !mongo.IsDuplicateKeyError(err) {
			return dtos.LinkedProvidersResponseModel{}, err
		}
		// A concurrent request linked first; report what it decided.
		if err := h.ensureLinkable(ctx, command.UserID, identity); err != nil && !errors.Is(err, errAlreadyLinkedHere) {
			return dtos.LinkedProvidersResponseModel{}, err
		}
	}
	return linkedProviders(ctx, h.identityRepository, command.UserID)
}

var errAlreadyLinkedHere = errors.New("provider account is already linked to this user")

// ensureLinkable rejects a provider account that belongs to another user and a
// second account of a provider the user already connected. It returns
// errAlreadyLinkedHere when this exact provider account is already the user's.
func (h *LinkSocialProviderHandler) ensureLinkable(ctx context.Context, userID string, identity authAbstract.VerifiedIdentity) error {
	owner, err := h.identityRepository.FindOneByFilter(ctx, bson.M{
		"provider": identity.Provider, "providerUserId": identity.ProviderUserID,
	})
	switch {
	case err == nil && owner.UserId == userID:
		return errAlreadyLinkedHere
	case err == nil:
		return helpers.NewConflictError("auth.error.provider_already_linked")
	case !helpers.IsApplicationError(err, "database.error.document_not_found"):
		return err
	}

	connected, err := h.identityRepository.ExistsByFilter(ctx, bson.M{"userId": userID, "provider": identity.Provider})
	if err != nil {
		return err
	}
	if connected {
		return helpers.NewConflictError("auth.error.provider_already_connected")
	}
	return nil
}

var _ cqrs.CommandHandler[LinkSocialProviderCommand, dtos.LinkedProvidersResponseModel] = (*LinkSocialProviderHandler)(nil)

// UnlinkSocialProviderCommand disconnects a provider from the signed-in user.
// The last way to sign in cannot be removed: a user without a password keeps
// at least one provider.
type UnlinkSocialProviderCommand struct {
	cqrs.Request[dtos.LinkedProvidersResponseModel]
	UserID   string
	Provider entities.AuthProvider
}

type UnlinkSocialProviderHandler struct {
	userRepository     databaseAbstract.DbRepository[entities.User]
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity]
	keepers            map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper
}

func NewUnlinkSocialProviderHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	keepers []authAbstract.SocialAuthorizationKeeper,
) *UnlinkSocialProviderHandler {
	handler := &UnlinkSocialProviderHandler{
		userRepository:     userRepository,
		identityRepository: identityRepository,
		keepers:            make(map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper, len(keepers)),
	}
	for _, keeper := range keepers {
		handler.keepers[keeper.Provider()] = keeper
	}
	return handler
}

func (h *UnlinkSocialProviderHandler) Handle(ctx context.Context, command UnlinkSocialProviderCommand) (dtos.LinkedProvidersResponseModel, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return dtos.LinkedProvidersResponseModel{}, helpers.NewLocalizedError("user.error.invalid_user_id")
	}

	var removed *entities.UserIdentity
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		removed = nil
		identity, err := h.identityRepository.FindOneByFilter(txCtx, bson.M{"userId": command.UserID, "provider": command.Provider})
		if helpers.IsApplicationError(err, "database.error.document_not_found") {
			return helpers.NewNotFoundError("auth.error.provider_not_linked")
		}
		if err != nil {
			return err
		}
		user, err := h.userRepository.FindByID(txCtx, userObjectID)
		if helpers.IsApplicationError(err, "database.error.document_not_found") {
			return helpers.NewNotFoundError("user.error.not_found")
		}
		if err != nil {
			return err
		}
		if user.HashPassword == "" {
			count, err := h.identityRepository.Collection().CountDocuments(txCtx, bson.M{"userId": command.UserID})
			if err != nil {
				return err
			}
			if count <= 1 {
				return helpers.NewConflictError("auth.error.last_sign_in_method")
			}
		}
		if err := h.identityRepository.Delete(txCtx, identity.Id); err != nil {
			return err
		}
		// Writing the user makes two concurrent unlinks conflict, so they cannot
		// each see the other's provider and remove both.
		if err := h.userRepository.UpdateFields(txCtx, userObjectID, bson.M{"updatedOn": time.Now()}); err != nil {
			return err
		}
		removed = identity
		return nil
	})
	if err != nil {
		return dtos.LinkedProvidersResponseModel{}, err
	}

	if keeper, ok := h.keepers[removed.Provider]; ok && removed.AppleRefreshTokenEnc != "" {
		// The link is already gone; a failed revoke is reported by the adapter.
		_ = keeper.Revoke(ctx, removed.AppleRefreshTokenEnc, removed.ProviderClientId)
	}
	return linkedProviders(ctx, h.identityRepository, command.UserID)
}

var _ cqrs.CommandHandler[UnlinkSocialProviderCommand, dtos.LinkedProvidersResponseModel] = (*UnlinkSocialProviderHandler)(nil)
