package commands

import (
	"context"

	"houseflowApi/internal/abstract"
	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type SendEmailVerificationCodeCommand struct {
	cqrs.Request[cqrs.NoResult]
	Email string
}

type SendEmailVerificationCodeHandler struct {
	userRepository *abstract.DbRepository[entities.User]
	emailSender    authAbstract.EmailSender
}

func NewSendEmailVerificationCodeHandler(
	userRepository *abstract.DbRepository[entities.User],
	emailSender authAbstract.EmailSender,
) *SendEmailVerificationCodeHandler {
	return &SendEmailVerificationCodeHandler{userRepository: userRepository, emailSender: emailSender}
}

func (h *SendEmailVerificationCodeHandler) Handle(ctx context.Context, command SendEmailVerificationCodeCommand) (cqrs.NoResult, error) {
	settings, err := config.MustLoadConfig()
	if err != nil {
		return cqrs.NoResult{}, err
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

	validityMinutes := settings.Internal.PasswordReset.ValidityMinutes
	window := helpers.ResetCodeWindow(validityMinutes)
	code := helpers.GenerateResetCode(command.Email, settings.Internal.PasswordReset.Secret, window)
	err = h.emailSender.SendEmailVerificationCode(command.Email, code, validityMinutes)
	return cqrs.NoResult{}, err
}

var _ cqrs.CommandHandler[SendEmailVerificationCodeCommand, cqrs.NoResult] = (*SendEmailVerificationCodeHandler)(nil)
