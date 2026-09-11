package commands

import (
	"context"
	"time"

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
	resetSecret     string
	validityMinutes int
}

func NewValidateEmailHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	resetSecret string,
	validityMinutes int,
) *ValidateEmailHandler {
	return &ValidateEmailHandler{
		userRepository:  userRepository,
		resetSecret:     resetSecret,
		validityMinutes: validityMinutes,
	}
}

func (h *ValidateEmailHandler) Handle(ctx context.Context, command ValidateEmailCommand) (cqrs.NoResult, error) {
	if !helpers.IsResetCodeValid(command.Email, command.Code, h.resetSecret, h.validityMinutes) {
		return cqrs.NoResult{}, helpers.NewLocalizedError("auth.error.invalid_or_expired_email_verification_code")
	}

	user, err := h.userRepository.FindByColumn(ctx, "email", command.Email)
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
