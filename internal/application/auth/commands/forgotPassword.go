package commands

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/config"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type ForgotPasswordCommand struct {
	cqrs.Request[cqrs.NoResult]
	Email string
}

type ForgotPasswordHandler struct {
	userRepository databaseAbstract.DbRepository[entities.User]
	emailSender    authAbstract.EmailSender
}

func NewForgotPasswordHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	emailSender authAbstract.EmailSender,
) *ForgotPasswordHandler {
	return &ForgotPasswordHandler{userRepository: userRepository, emailSender: emailSender}
}

func (h *ForgotPasswordHandler) Handle(ctx context.Context, command ForgotPasswordCommand) (cqrs.NoResult, error) {
	settings, err := config.MustLoadConfig()
	if err != nil {
		return cqrs.NoResult{}, err
	}

	user, err := h.userRepository.FindByColumn(ctx, "email", command.Email)
	if err != nil {
		if user == nil || helpers.IsApplicationError(err, "database.error.document_not_found") {
			return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.not_found")
		}
		return cqrs.NoResult{}, err
	}

	validityMinutes := settings.Internal.PasswordReset.ValidityMinutes
	window := helpers.ResetCodeWindow(validityMinutes)
	code := helpers.GenerateResetCode(command.Email, settings.Internal.PasswordReset.Secret, window)
	if err := h.emailSender.SendResetCodeEmail(command.Email, code, validityMinutes); err != nil {
		return cqrs.NoResult{}, err
	}
	return cqrs.NoResult{}, nil
}

var _ cqrs.CommandHandler[ForgotPasswordCommand, cqrs.NoResult] = (*ForgotPasswordHandler)(nil)
