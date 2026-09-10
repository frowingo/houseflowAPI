package commands

import (
	"context"
	"time"

	"houseflowApi/internal/config"
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
	userRepository databaseAbstract.DbRepository[entities.User]
}

func NewResetPasswordHandler(userRepository databaseAbstract.DbRepository[entities.User]) *ResetPasswordHandler {
	return &ResetPasswordHandler{userRepository: userRepository}
}

func (h *ResetPasswordHandler) Handle(ctx context.Context, command ResetPasswordCommand) (cqrs.NoResult, error) {
	settings, err := config.MustLoadConfig()
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if !helpers.IsResetCodeValid(command.Email, command.Code, settings.Internal.PasswordReset.Secret, settings.Internal.PasswordReset.ValidityMinutes) {
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
