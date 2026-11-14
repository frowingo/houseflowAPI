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

type ResetPasswordCommand struct {
	cqrs.Request[cqrs.NoResult]
	Email       string
	Code        string
	NewPassword string
}

type ResetPasswordHandler struct {
	userRepository  databaseAbstract.DbRepository[entities.User]
	codeAttempts    *authPolicies.CodeAttempts
	resetSecret     string
	validityMinutes int
}

func NewResetPasswordHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	codeAttempts *authPolicies.CodeAttempts,
	resetSecret string,
	validityMinutes int,
) *ResetPasswordHandler {
	return &ResetPasswordHandler{
		userRepository:  userRepository,
		codeAttempts:    codeAttempts,
		resetSecret:     resetSecret,
		validityMinutes: validityMinutes,
	}
}

func (h *ResetPasswordHandler) Handle(ctx context.Context, command ResetPasswordCommand) (cqrs.NoResult, error) {
	codeCheck := authPolicies.CodeCheck{
		Purpose: authPolicies.CodePurposeResetPassword, Email: command.Email,
		Subject: helpers.NormalizeEmail(command.Email), Code: command.Code,
		Secret: h.resetSecret, ValidityMinutes: h.validityMinutes,
	}
	result, err := h.codeAttempts.Verify(ctx, codeCheck)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if !result.Valid {
		if result.Consumed {
			applied, err := h.alreadyApplied(ctx, command)
			if err != nil || applied {
				return cqrs.NoResult{}, err
			}
		}
		return cqrs.NoResult{}, result.Refusal("auth.error.invalid_or_expired_reset_code")
	}

	user, err := h.userRepository.FindOneByFilter(ctx, helpers.EmailLookupFilter(command.Email))
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
	// Consumed before the password changes: a failure here leaves the old
	// password, never a code that can reset it again.
	if err := h.codeAttempts.Consume(ctx, codeCheck); err != nil {
		return cqrs.NoResult{}, err
	}
	err = h.userRepository.UpdateFields(ctx, user.Id, bson.M{
		"password": hashedPassword, "isActive": true,
		"failedLoginAttempts": 0, "updatedOn": time.Now(),
	})
	return cqrs.NoResult{}, err
}

// alreadyApplied reports whether command repeats a reset that went through,
// say one whose response was lost. It needs the genuine code as well as the
// password now stored, and changes nothing. The cheap code check comes first,
// so a wrong code costs no user read or bcrypt.
func (h *ResetPasswordHandler) alreadyApplied(ctx context.Context, command ResetPasswordCommand) (bool, error) {
	window := helpers.ResetCodeWindow(h.validityMinutes)
	if _, matched := helpers.MatchResetCode(helpers.NormalizeEmail(command.Email), command.Code, h.resetSecret, window, h.validityMinutes); !matched {
		return false, nil
	}
	user, err := h.userRepository.FindOneByFilter(ctx, helpers.EmailLookupFilter(command.Email))
	if err != nil || user == nil {
		return false, err
	}
	return helpers.CheckPasswordHash(command.NewPassword, user.HashPassword), nil
}

var _ cqrs.CommandHandler[ResetPasswordCommand, cqrs.NoResult] = (*ResetPasswordHandler)(nil)
