package commands

import (
	"context"
	"time"

	authPolicies "houseflowApi/internal/application/auth/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
)

type ValidateEmailCommand struct {
	cqrs.Request[cqrs.NoResult]
	Email string
	Code  string
}

type ValidateEmailHandler struct {
	userRepository  databaseAbstract.DbRepository[entities.User]
	codeAttempts    *authPolicies.CodeAttempts
	resetSecret     string
	validityMinutes int
}

func NewValidateEmailHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	codeAttempts *authPolicies.CodeAttempts,
	resetSecret string,
	validityMinutes int,
) *ValidateEmailHandler {
	return &ValidateEmailHandler{
		userRepository:  userRepository,
		codeAttempts:    codeAttempts,
		resetSecret:     resetSecret,
		validityMinutes: validityMinutes,
	}
}

func (h *ValidateEmailHandler) Handle(ctx context.Context, command ValidateEmailCommand) (cqrs.NoResult, error) {
	valid, err := h.codeAttempts.Verify(ctx, authPolicies.CodeCheck{
		Purpose: authPolicies.CodePurposeVerifyEmail, Email: command.Email,
		Subject: helpers.NormalizeEmail(command.Email), Code: command.Code,
		Secret: h.resetSecret, ValidityMinutes: h.validityMinutes,
	})
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if !valid {
		return cqrs.NoResult{}, helpers.NewLocalizedError("auth.error.invalid_or_expired_email_verification_code")
	}

	user, err := h.userRepository.FindOneByFilter(ctx, helpers.EmailLookupFilter(command.Email))
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if user == nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.not_found")
	}
	if user.IsVerifyEmail {
		return cqrs.NoResult{}, nil
	}

	err = h.userRepository.UpdateFields(ctx, user.Id, bson.M{
		"isVerifyEmail": true, "updatedOn": time.Now(),
	})
	return cqrs.NoResult{}, err
}

var _ cqrs.CommandHandler[ValidateEmailCommand, cqrs.NoResult] = (*ValidateEmailHandler)(nil)
