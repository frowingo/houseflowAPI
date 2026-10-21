package commands

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	authPolicies "houseflowApi/internal/application/auth/policies"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

// SocialAuthCommand signs a user in with a provider credential, creating the
// account on first use. The provider only proves the identity; the session is
// our own JWT. When the provider shares no email, the reply carries a pending
// token instead and the sign-up is finished with CompleteSocialSignUpCommand.
type SocialAuthCommand struct {
	cqrs.Request[dtos.SocialAuthResponseModel]
	Provider   entities.AuthProvider
	Credential authAbstract.SocialCredential
	// Firstname and Lastname are used only when the account is created. Apple
	// gives the name to the client on the first authorization only.
	Firstname string
	Lastname  string
	Language  string
}

type SocialAuthHandler struct {
	accounts      *authPolicies.SocialAccounts
	pendingTokens *helpers.SocialPendingTokenService
	verifiers     map[entities.AuthProvider]authAbstract.SocialIdentityVerifier
	keepers       map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper
}

// NewSocialAuthHandler takes the verifiers of the enabled providers; a
// provider without a verifier is reported as not supported.
func NewSocialAuthHandler(
	accounts *authPolicies.SocialAccounts,
	pendingTokens *helpers.SocialPendingTokenService,
	verifiers []authAbstract.SocialIdentityVerifier,
	keepers []authAbstract.SocialAuthorizationKeeper,
) *SocialAuthHandler {
	handler := &SocialAuthHandler{
		accounts:      accounts,
		pendingTokens: pendingTokens,
		verifiers:     make(map[entities.AuthProvider]authAbstract.SocialIdentityVerifier, len(verifiers)),
		keepers:       make(map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper, len(keepers)),
	}
	for _, verifier := range verifiers {
		handler.verifiers[verifier.Provider()] = verifier
	}
	for _, keeper := range keepers {
		handler.keepers[keeper.Provider()] = keeper
	}
	return handler
}

func (h *SocialAuthHandler) Handle(ctx context.Context, command SocialAuthCommand) (dtos.SocialAuthResponseModel, error) {
	verifier, ok := h.verifiers[command.Provider]
	if !ok {
		return dtos.SocialAuthResponseModel{}, helpers.NewLocalizedError("auth.error.provider_not_supported")
	}
	identity, err := verifier.Verify(ctx, command.Credential)
	if err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}

	result, signedIn, err := h.accounts.SignIn(ctx, identity)
	if signedIn || err != nil {
		return result, err
	}

	keeper, keeps := h.keepers[identity.Provider]
	if identity.Email == "" {
		// A provider whose authorization must be kept (Apple) hands out a
		// one-time code that would expire during the email step; Apple always
		// shares an email when the email scope is requested.
		if keeps {
			return dtos.SocialAuthResponseModel{}, helpers.NewLocalizedError("auth.error.social_email_required")
		}
		pendingToken, err := h.pendingTokens.Issue(helpers.PendingSocialSignUp{
			Provider: identity.Provider, ProviderUserID: identity.ProviderUserID, ClientID: identity.ClientID,
			FirstName: authPolicies.SocialName(command.Firstname, identity.FirstName),
			LastName:  authPolicies.SocialName(command.Lastname, identity.LastName),
		})
		if err != nil {
			return dtos.SocialAuthResponseModel{}, err
		}
		return dtos.SocialAuthResponseModel{NeedsEmail: true, PendingToken: pendingToken}, nil
	}

	if err := h.accounts.EnsureEmailAvailable(ctx, identity.Email); err != nil {
		return dtos.SocialAuthResponseModel{}, err
	}
	keptToken := ""
	if keeps {
		keptToken, err = keeper.Keep(ctx, identity, command.Credential)
		if err != nil {
			return dtos.SocialAuthResponseModel{}, err
		}
	}
	return h.accounts.Create(ctx, authPolicies.NewSocialAccount{
		Identity: identity, Email: identity.Email, EmailVerified: identity.EmailVerified,
		Firstname: command.Firstname, Lastname: command.Lastname, Language: command.Language,
		KeptToken: keptToken,
	})
}

var _ cqrs.CommandHandler[SocialAuthCommand, dtos.SocialAuthResponseModel] = (*SocialAuthHandler)(nil)
