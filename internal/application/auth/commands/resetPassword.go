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

type ResetPasswordCommand struct {
	cqrs.Request[cqrs.NoResult]
	Email       string
	Code        string
	NewPassword string
}

type ResetPasswordHandler struct {
	userRepository  databaseAbstract.DbRepository[entities.User]
	resetSecret     string
	validityMinutes int
}

func NewResetPasswordHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	resetSecret string,
	validityMinutes int,
) *ResetPasswordHandler {
	return &ResetPasswordHandler{
		userRepository:  userRepository,
		resetSecret:     resetSecret,
		validityMinutes: validityMinutes,
	}
}

func (h *ResetPasswordHandler) Handle(ctx context.Context, command ResetPasswordCommand) (cqrs.NoResult, error) {
	if !helpers.IsResetCodeValid(command.Email, command.Code, h.resetSecret, h.validityMinutes) {
		return cqrs.NoResult{}, helpers.NewLocalizedError("auth.error.invalid_or_expired_reset_code")
	}

	user, err := h.userRepository.FindByColumn(ctx, "email", command.Email)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if user == nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.not_found")
	}

	hashedPassword, err := helpers.HashPassword(command.NewPassword)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	err = h.userRepository.UpdateFields(ctx, user.Id, bson.M{
		"password": hashedPassword, "isActive": true,
		"failedLoginAttempts": 0, "updatedOn": time.Now(),
	})
	return cqrs.NoResult{}, err
}

var _ cqrs.CommandHandler[ResetPasswordCommand, cqrs.NoResult] = (*ResetPasswordHandler)(nil)
