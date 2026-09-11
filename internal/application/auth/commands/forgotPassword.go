package commands

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
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
	userRepository  databaseAbstract.DbRepository[entities.User]
	emailSender     authAbstract.EmailSender
	resetSecret     string
	validityMinutes int
}

func NewForgotPasswordHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	emailSender authAbstract.EmailSender,
	resetSecret string,
	validityMinutes int,
) *ForgotPasswordHandler {
	return &ForgotPasswordHandler{
		userRepository:  userRepository,
		emailSender:     emailSender,
		resetSecret:     resetSecret,
		validityMinutes: validityMinutes,
	}
}

func (h *ForgotPasswordHandler) Handle(ctx context.Context, command ForgotPasswordCommand) (cqrs.NoResult, error) {
	user, err := h.userRepository.FindByColumn(ctx, "email", command.Email)
	if err != nil {
		if user == nil || helpers.IsApplicationError(err, "database.error.document_not_found") {
			return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.not_found")
		}
		return cqrs.NoResult{}, err
	}

	window := helpers.ResetCodeWindow(h.validityMinutes)
	code := helpers.GenerateResetCode(command.Email, h.resetSecret, window)
	if err := h.emailSender.SendResetCodeEmail(command.Email, code, h.validityMinutes); err != nil {
		return cqrs.NoResult{}, err
	}
	return cqrs.NoResult{}, nil
}

var _ cqrs.CommandHandler[ForgotPasswordCommand, cqrs.NoResult] = (*ForgotPasswordHandler)(nil)
