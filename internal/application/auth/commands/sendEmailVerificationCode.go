package commands

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type SendEmailVerificationCodeCommand struct {
	cqrs.Request[cqrs.NoResult]
	Email string
}

type SendEmailVerificationCodeHandler struct {
	userRepository  databaseAbstract.DbRepository[entities.User]
	emailSender     authAbstract.EmailSender
	resetSecret     string
	validityMinutes int
}

func NewSendEmailVerificationCodeHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	emailSender authAbstract.EmailSender,
	resetSecret string,
	validityMinutes int,
) *SendEmailVerificationCodeHandler {
	return &SendEmailVerificationCodeHandler{
		userRepository:  userRepository,
		emailSender:     emailSender,
		resetSecret:     resetSecret,
		validityMinutes: validityMinutes,
	}
}

func (h *SendEmailVerificationCodeHandler) Handle(ctx context.Context, command SendEmailVerificationCodeCommand) (cqrs.NoResult, error) {
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

	window := helpers.ResetCodeWindow(h.validityMinutes)
	code := helpers.GenerateResetCode(command.Email, h.resetSecret, window)
	err = h.emailSender.SendEmailVerificationCode(command.Email, code, h.validityMinutes)
	return cqrs.NoResult{}, err
}

var _ cqrs.CommandHandler[SendEmailVerificationCodeCommand, cqrs.NoResult] = (*SendEmailVerificationCodeHandler)(nil)
