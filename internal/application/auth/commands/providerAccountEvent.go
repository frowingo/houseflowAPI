package commands

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	userPolicies "houseflowApi/internal/application/user/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

// ProviderAccountEventCommand applies a notification a provider pushes about
// one of its users (Sign in with Apple server-to-server notifications):
//   - consent revoked: the provider link is removed, the account stays;
//   - account deleted at the provider: our account is deleted, as for a
//     Facebook data deletion request (ADR-025);
//   - email forwarding changes and unknown events: acknowledged, nothing changes.
//
// Providers may deliver the same event more than once, so every step is
// idempotent.
type ProviderAccountEventCommand struct {
	cqrs.Request[dtos.ProviderDataDeletionResult]
	Provider entities.AuthProvider
	Payload  string
}

type ProviderAccountEventHandler struct {
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity]
	accounts           *userPolicies.AccountRemoval
	verifiers          map[entities.AuthProvider]authAbstract.ProviderNotificationVerifier
}

func NewProviderAccountEventHandler(
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	accounts *userPolicies.AccountRemoval,
	verifiers []authAbstract.ProviderNotificationVerifier,
) *ProviderAccountEventHandler {
	handler := &ProviderAccountEventHandler{
		identityRepository: identityRepository,
		accounts:           accounts,
		verifiers:          make(map[entities.AuthProvider]authAbstract.ProviderNotificationVerifier, len(verifiers)),
	}
	for _, verifier := range verifiers {
		handler.verifiers[verifier.Provider()] = verifier
	}
	return handler
}

func (h *ProviderAccountEventHandler) Handle(ctx context.Context, command ProviderAccountEventCommand) (dtos.ProviderDataDeletionResult, error) {
	verifier, ok := h.verifiers[command.Provider]
	if !ok {
		return dtos.ProviderDataDeletionResult{}, helpers.NewLocalizedError("auth.error.provider_not_supported")
	}
	event, err := verifier.VerifyNotification(ctx, command.Payload)
	if err != nil {
		return dtos.ProviderDataDeletionResult{}, err
	}
	if event.Type != authAbstract.ProviderConsentRevoked && event.Type != authAbstract.ProviderAccountDeleted {
		return dtos.ProviderDataDeletionResult{}, nil
	}

	identity, err := h.identityRepository.FindOneByFilter(ctx, bson.M{
		"provider": command.Provider, "providerUserId": event.ProviderUserID,
	})
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		return dtos.ProviderDataDeletionResult{}, nil
	}
	if err != nil {
		return dtos.ProviderDataDeletionResult{}, err
	}

	if event.Type == authAbstract.ProviderConsentRevoked {
		// The user already withdrew the authorization at the provider, so
		// there is nothing to revoke; the account keeps its other sign-in methods.
		if err := h.identityRepository.Delete(ctx, identity.Id); err != nil &&
			!helpers.IsApplicationError(err, "database.error.delete_not_found") {
			return dtos.ProviderDataDeletionResult{}, err
		}
		return dtos.ProviderDataDeletionResult{UserID: identity.UserId}, nil
	}

	houseIDs, err := h.accounts.Remove(ctx, identity.UserId)
	if helpers.IsApplicationError(err, "database.error.delete_not_found") {
		if err := h.identityRepository.Delete(ctx, identity.Id); err != nil &&
			!helpers.IsApplicationError(err, "database.error.delete_not_found") {
			return dtos.ProviderDataDeletionResult{}, err
		}
		return dtos.ProviderDataDeletionResult{}, nil
	}
	if err != nil {
		return dtos.ProviderDataDeletionResult{}, err
	}
	return dtos.ProviderDataDeletionResult{UserID: identity.UserId, HouseIDs: houseIDs}, nil
}

var _ cqrs.CommandHandler[ProviderAccountEventCommand, dtos.ProviderDataDeletionResult] = (*ProviderAccountEventHandler)(nil)
