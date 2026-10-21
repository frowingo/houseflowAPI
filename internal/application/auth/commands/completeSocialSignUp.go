package commands

import (
	"context"
	"strconv"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	authPolicies "houseflowApi/internal/application/auth/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

// An email-less social sign-up (Snapchat, some Facebook accounts) is finished
// in two steps: SendSocialSignUpCodeCommand mails a code to the address the
// user typed, CompleteSocialSignUpCommand checks it and creates the account.
// The account exists only once the email is proven, so nobody can hold an
// address they do not own through a provider login.

// socialSignUpCodeSubject binds a code to one pending identity and one email,
// and keeps it apart from password reset and email verification codes.
func socialSignUpCodeSubject(pending helpers.PendingSocialSignUp, email string) string {
	return "social-signup|" + strconv.Itoa(int(pending.Provider)) + "|" + pending.ProviderUserID + "|" + helpers.NormalizeEmail(email)
}

type SendSocialSignUpCodeCommand struct {
	cqrs.Request[cqrs.NoResult]
	PendingToken string
	Email        string
}

type SendSocialSignUpCodeHandler struct {
	accounts        *authPolicies.SocialAccounts
	pendingTokens   *helpers.SocialPendingTokenService
	emailSender     authAbstract.EmailSender
	codeSecret      string
	validityMinutes int
}

func NewSendSocialSignUpCodeHandler(
	accounts *authPolicies.SocialAccounts,
	pendingTokens *helpers.SocialPendingTokenService,
	emailSender authAbstract.EmailSender,
	codeSecret string,
	validityMinutes int,
) *SendSocialSignUpCodeHandler {
	return &SendSocialSignUpCodeHandler{
		accounts: accounts, pendingTokens: pendingTokens, emailSender: emailSender,
		codeSecret: codeSecret, validityMinutes: validityMinutes,
	}
}

func (h *SendSocialSignUpCodeHandler) Handle(ctx context.Context, command SendSocialSignUpCodeCommand) (cqrs.NoResult, error) {
	pending, err := h.pendingTokens.Parse(command.PendingToken)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if err := h.accounts.EnsureEmailAvailable(ctx, command.Email); err != nil {
		return cqrs.NoResult{}, err
	}
	window := helpers.ResetCodeWindow(h.validityMinutes)
	code := helpers.GenerateResetCode(socialSignUpCodeSubject(pending, command.Email), h.codeSecret, window)
	return cqrs.NoResult{}, h.emailSender.SendEmailVerificationCode(helpers.NormalizeEmail(command.Email), code, h.validityMinutes)
}

var _ cqrs.CommandHandler[SendSocialSignUpCodeCommand, cqrs.NoResult] = (*SendSocialSignUpCodeHandler)(nil)

type CompleteSocialSignUpCommand struct {
	cqrs.Request[dtos.SocialAuthResponseModel]
	PendingToken string
	Email        string
	Code         string
	Firstname    string
	Lastname     string
	Language     string
}

type CompleteSocialSignUpHandler struct {
	accounts        *authPolicies.SocialAccounts
	pendingTokens   *helpers.SocialPendingTokenService
	codeSecret      string
	validityMinutes int
}

func NewCompleteSocialSignUpHandler(
	accounts *authPolicies.SocialAccounts,
	pendingTokens *helpers.SocialPendingTokenService,
	codeSecret string,
	validityMinutes int,
) *CompleteSocialSignUpHandler {
	return &CompleteSocialSignUpHandler{
		accounts: accounts, pendingTokens: pendingTokens,
		codeSecret: codeSecret, validityMinutes: validityMinutes,
	}
}

func (h *CompleteSocialSignUpHandler) Handle(ctx context.Context, command CompleteSocialSignUpCommand) (dtos.SocialAuthResponseModel, error) {
	pending, err := h.pendingTokens.Parse(command.PendingToken)
	if err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}
	if !helpers.IsResetCodeValid(socialSignUpCodeSubject(pending, command.Email), command.Code, h.codeSecret, h.validityMinutes) {
		return dtos.SocialAuthResponseModel{}, helpers.NewLocalizedError("auth.error.invalid_or_expired_email_verification_code")
	}

	identity := authAbstract.VerifiedIdentity{
		Provider: pending.Provider, ProviderUserID: pending.ProviderUserID, ClientID: pending.ClientID,
		FirstName: pending.FirstName, LastName: pending.LastName,
	}
	// The same provider account may have finished signing up meanwhile.
	result, signedIn, err := h.accounts.SignIn(ctx, identity)
	if signedIn || err != nil {
		return result, err
	}
	if err := h.accounts.EnsureEmailAvailable(ctx, command.Email); err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}
	return h.accounts.Create(ctx, authPolicies.NewSocialAccount{
		Identity: identity, Email: command.Email, EmailVerified: true,
		Firstname: command.Firstname, Lastname: command.Lastname, Language: command.Language,
	})
}

var _ cqrs.CommandHandler[CompleteSocialSignUpCommand, dtos.SocialAuthResponseModel] = (*CompleteSocialSignUpHandler)(nil)
