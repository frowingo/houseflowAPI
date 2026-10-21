package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	userPolicies "houseflowApi/internal/application/user/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

// dataDeletionRecordRetention is how long a confirmation code can be looked up.
const dataDeletionRecordRetention = 90 * 24 * time.Hour

// ProviderDataDeletionCommand handles a provider's request to delete the data
// of a user who removed our app (Facebook's data deletion callback). The
// linked account is always deleted, whatever other sign-in methods it has
// (ADR-025).
type ProviderDataDeletionCommand struct {
	cqrs.Request[dtos.ProviderDataDeletionResult]
	Provider      entities.AuthProvider
	SignedRequest string
}

type ProviderDataDeletionHandler struct {
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity]
	deletionRepository databaseAbstract.DbRepository[entities.DataDeletionRequest]
	accounts           *userPolicies.AccountRemoval
	verifiers          map[entities.AuthProvider]authAbstract.DataDeletionRequestVerifier
}

func NewProviderDataDeletionHandler(
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	deletionRepository databaseAbstract.DbRepository[entities.DataDeletionRequest],
	accounts *userPolicies.AccountRemoval,
	verifiers []authAbstract.DataDeletionRequestVerifier,
) *ProviderDataDeletionHandler {
	handler := &ProviderDataDeletionHandler{
		identityRepository: identityRepository,
		deletionRepository: deletionRepository,
		accounts:           accounts,
		verifiers:          make(map[entities.AuthProvider]authAbstract.DataDeletionRequestVerifier, len(verifiers)),
	}
	for _, verifier := range verifiers {
		handler.verifiers[verifier.Provider()] = verifier
	}
	return handler
}

func (h *ProviderDataDeletionHandler) Handle(ctx context.Context, command ProviderDataDeletionCommand) (dtos.ProviderDataDeletionResult, error) {
	verifier, ok := h.verifiers[command.Provider]
	if !ok {
		return dtos.ProviderDataDeletionResult{}, helpers.NewLocalizedError("auth.error.provider_not_supported")
	}
	providerUserID, err := verifier.VerifyDataDeletionRequest(command.SignedRequest)
	if err != nil {
		return dtos.ProviderDataDeletionResult{}, err
	}

	var result dtos.ProviderDataDeletionResult
	identity, err := h.identityRepository.FindOneByFilter(ctx, bson.M{
		"provider": command.Provider, "providerUserId": providerUserID,
	})
	switch {
	case err == nil:
		houseIDs, err := h.accounts.Remove(ctx, identity.UserId)
		switch {
		case err == nil:
			result.UserID, result.HouseIDs = identity.UserId, houseIDs
		case helpers.IsApplicationError(err, "database.error.delete_not_found"):
			// The user is already gone; only the stale link is left.
			if err := h.identityRepository.Delete(ctx, identity.Id); err != nil &&
				!helpers.IsApplicationError(err, "database.error.delete_not_found") {
				return dtos.ProviderDataDeletionResult{}, err
			}
		default:
			return dtos.ProviderDataDeletionResult{}, err
		}
	case helpers.IsApplicationError(err, "database.error.document_not_found"):
		// Nothing is stored for this provider account; the request still
		// gets a confirmation code, as Facebook requires.
	default:
		return dtos.ProviderDataDeletionResult{}, err
	}

	code, err := newConfirmationCode()
	if err != nil {
		return dtos.ProviderDataDeletionResult{}, err
	}
	now := time.Now().UTC()
	if _, err := h.deletionRepository.Insert(ctx, entities.DataDeletionRequest{
		Id: code, Provider: command.Provider, Status: entities.DataDeletionCompleted,
		AccountDeleted: result.UserID != "", RequestedOn: now, ExpiresAt: now.Add(dataDeletionRecordRetention),
	}); err != nil {
		return dtos.ProviderDataDeletionResult{}, err
	}
	result.ConfirmationCode = code
	return result, nil
}

func newConfirmationCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

var _ cqrs.CommandHandler[ProviderDataDeletionCommand, dtos.ProviderDataDeletionResult] = (*ProviderDataDeletionHandler)(nil)
