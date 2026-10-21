package controllers

import (
	"houseflowApi/external/validator"
	authAbstract "houseflowApi/internal/application/auth/abstract"
	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type AuthController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

func NewAuthController(sender cqrs.Sender, localizers ...helpers.MessageLocalizer) *AuthController {
	var localizer helpers.MessageLocalizer
	if len(localizers) > 0 {
		localizer = localizers[0]
	}
	return &AuthController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
	}
}

// @Summary Check if token is valid
// @Description Returns true if the token has not expired, false otherwise
// @Tags Auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} dtos.IsAuthResponseModel
// @Failure 400 {object} dtos.IsAuthResponseModel
// @Router /auth/isAuth [get]
func (r *AuthController) IsAuth(c *fiber.Ctx) error {

	authHeader := c.Get("Authorization")
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.IsAuthResponseModel{Success: false, Data: nil})
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	user, err := cqrs.Send[*dtos.AuthUserResultModel](ctx, r.sender, authQueries.ValidateAuthQuery{Token: parts[1]})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.IsAuthResponseModel{Success: false, Data: nil})
	}

	return c.Status(fiber.StatusOK).JSON(dtos.IsAuthResponseModel{Success: true, Data: user})
}

// @Summary User Login
// @Description Login with email and password to receive JWT token
// @Tags Auth
// @Accept json
// @Produce json
// @Param login body dtos.LoginRequestModel true "Login credentials"
// @Success 200 {object} map[string]string "Returns JWT token"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Invalid credentials"
// @Router /auth/login [post]
func (r *AuthController) Login(c *fiber.Ctx) error {

	model := new(dtos.LoginRequestModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if model.Email == "" || model.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.email_password_required"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	token, err := cqrs.Send[string](ctx, r.sender, authCommands.LoginCommand{
		Email: model.Email, Password: model.Password,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	if token == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.invalid_credentials"))
	} else {
		return c.Status(200).JSON(fiber.Map{"token": token})
	}
}

// @Summary User Signup
// @Description Signup with email, password, firstname, and lastname to receive JWT token
// @Tags Auth
// @Accept json
// @Produce json
// @Param signup body dtos.SignUpUserModel true "Signup request"
// @Success 201 {object} map[string]string "Returns JWT token"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Invalid credentials"
// @Router /auth/signup [post]
func (r *AuthController) Signup(c *fiber.Ctx) error {

	model := new(dtos.SignUpUserModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	token, err := cqrs.Send[string](ctx, r.sender, authCommands.SignUpCommand{
		Email: model.Email, Password: model.Password,
		Firstname: model.Firstname, Lastname: model.Lastname,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(201).JSON(fiber.Map{"token": token})
}

// @Summary Social Sign-In
// @Description Signs in with a Google, Apple or Facebook credential, creating the account on first use. Google sends idToken (and nonce if one was used). Apple sends idToken, the raw nonce whose SHA-256 hex digest was given to Apple, and on the first authorization authorizationCode, firstName and lastName. Facebook sends accessToken (classic login) or idToken with the nonce given to the SDK (iOS Limited Login). Snapchat sends the Login Kit accessToken. Returns 201 when a new account was created. When the provider shares no email the reply is 200 with needsEmail and a pendingToken: request a code with /auth/social/complete/code, then finish with /auth/social/complete.
// @Tags Auth
// @Accept json
// @Produce json
// @Param provider path string true "Provider" Enums(google, apple, facebook, snapchat)
// @Param body body dtos.SocialAuthRequestModel true "Provider credential"
// @Success 200 {object} dtos.SocialAuthResponseModel "Signed in to an existing account"
// @Success 201 {object} dtos.SocialAuthResponseModel "New account created"
// @Failure 400 {object} map[string]interface{} "Bad request, provider not supported or email missing"
// @Failure 401 {object} map[string]interface{} "Provider credential could not be verified"
// @Failure 409 {object} map[string]interface{} "Email registered with another sign-in method"
// @Failure 503 {object} map[string]interface{} "Provider temporarily unavailable"
// @Router /auth/social/{provider} [post]
func (r *AuthController) SocialAuth(c *fiber.Ctx) error {
	provider, ok := entities.ParseSocialProvider(strings.ToLower(c.Params("provider")))
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.provider_not_supported"))
	}

	model := new(dtos.SocialAuthRequestModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[dtos.SocialAuthResponseModel](ctx, r.sender, authCommands.SocialAuthCommand{
		Provider: provider,
		Credential: authAbstract.SocialCredential{
			IDToken: model.IDToken, AccessToken: model.AccessToken,
			Nonce: model.Nonce, AuthorizationCode: model.AuthorizationCode,
		},
		Firstname: model.Firstname, Lastname: model.Lastname, Language: model.Language,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	status := fiber.StatusOK
	if result.IsNewUser {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(result)
}

// @Summary Link Social Provider
// @Description Connects a provider account to the signed-in user so they can also sign in with it. The body is the same provider credential as /auth/social/{provider}; Apple also needs authorizationCode. Returns the providers now linked.
// @Tags Auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param provider path string true "Provider" Enums(google, apple, facebook, snapchat)
// @Param body body dtos.SocialAuthRequestModel true "Provider credential"
// @Success 200 {object} dtos.LinkedProvidersResponseModel
// @Failure 400 {object} map[string]interface{} "Bad request or provider not supported"
// @Failure 401 {object} map[string]interface{} "Unauthorized or credential not verified"
// @Failure 409 {object} map[string]interface{} "Provider account linked to another user, or another account of this provider already connected"
// @Router /auth/social/{provider}/link [post]
func (r *AuthController) LinkSocialProvider(c *fiber.Ctx) error {
	userID, ok := c.Locals("userID").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.unauthorized"))
	}
	provider, ok := entities.ParseSocialProvider(strings.ToLower(c.Params("provider")))
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.provider_not_supported"))
	}
	model := new(dtos.SocialAuthRequestModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[dtos.LinkedProvidersResponseModel](ctx, r.sender, authCommands.LinkSocialProviderCommand{
		UserID: userID, Provider: provider,
		Credential: authAbstract.SocialCredential{
			IDToken: model.IDToken, AccessToken: model.AccessToken,
			Nonce: model.Nonce, AuthorizationCode: model.AuthorizationCode,
		},
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(result)
}

// @Summary Unlink Social Provider
// @Description Disconnects a provider from the signed-in user. The last way to sign in cannot be removed: without a password at least one provider stays. A linked Apple authorization is revoked.
// @Tags Auth
// @Produce json
// @Security BearerAuth
// @Param provider path string true "Provider" Enums(google, apple, facebook, snapchat)
// @Success 200 {object} dtos.LinkedProvidersResponseModel
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Provider not linked"
// @Failure 409 {object} map[string]interface{} "Last sign-in method"
// @Router /auth/social/{provider}/link [delete]
func (r *AuthController) UnlinkSocialProvider(c *fiber.Ctx) error {
	userID, ok := c.Locals("userID").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.unauthorized"))
	}
	provider, ok := entities.ParseSocialProvider(strings.ToLower(c.Params("provider")))
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.provider_not_supported"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[dtos.LinkedProvidersResponseModel](ctx, r.sender, authCommands.UnlinkSocialProviderCommand{
		UserID: userID, Provider: provider,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(result)
}

// @Summary Apple Server-to-Server Notification
// @Description Endpoint registered in the Apple Developer portal for Sign in with Apple notifications. The JSON field payload is a JWT signed by Apple. consent-revoked removes the Apple link; account-delete deletes the account; email events are acknowledged without changes.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body dtos.AppleNotificationRequestModel true "Apple notification"
// @Success 200 {object} dtos.SuccessResponseModel
// @Failure 400 {object} map[string]interface{} "Apple sign-in is not enabled"
// @Failure 401 {object} map[string]interface{} "Payload not verified"
// @Router /auth/apple/notifications [post]
func (r *AuthController) AppleNotification(c *fiber.Ctx) error {
	model := new(dtos.AppleNotificationRequestModel)
	if err := c.BodyParser(model); err != nil || model.Payload == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[dtos.ProviderDataDeletionResult](ctx, r.sender, authCommands.ProviderAccountEventCommand{
		Provider: entities.ProviderApple, Payload: model.Payload,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}

// @Summary Send Social Sign-Up Code
// @Description Second step of an email-less social sign-up: mails a 6-character code to the email the user typed. The code is bound to the pending sign-up and to that email.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body dtos.SocialSignUpCodeRequestModel true "Pending sign-up and email"
// @Success 200 {object} dtos.SuccessResponseModel "Code sent"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Pending sign-up expired"
// @Failure 409 {object} map[string]interface{} "Email already registered"
// @Router /auth/social/complete/code [post]
func (r *AuthController) SendSocialSignUpCode(c *fiber.Ctx) error {
	model := new(dtos.SocialSignUpCodeRequestModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, authCommands.SendSocialSignUpCodeCommand{
		PendingToken: model.PendingToken, Email: model.Email,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}

// @Summary Complete Social Sign-Up
// @Description Last step of an email-less social sign-up: checks the emailed code and creates the account with a verified email. Returns 201 with our token; 200 when the provider account finished signing up meanwhile.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body dtos.CompleteSocialSignUpRequestModel true "Pending sign-up, email and code"
// @Success 200 {object} dtos.SocialAuthResponseModel "Signed in to an existing account"
// @Success 201 {object} dtos.SocialAuthResponseModel "New account created"
// @Failure 400 {object} map[string]interface{} "Bad request or invalid code"
// @Failure 401 {object} map[string]interface{} "Pending sign-up expired"
// @Failure 409 {object} map[string]interface{} "Email already registered"
// @Router /auth/social/complete [post]
func (r *AuthController) CompleteSocialSignUp(c *fiber.Ctx) error {
	model := new(dtos.CompleteSocialSignUpRequestModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[dtos.SocialAuthResponseModel](ctx, r.sender, authCommands.CompleteSocialSignUpCommand{
		PendingToken: model.PendingToken, Email: model.Email, Code: model.Code,
		Firstname: model.Firstname, Lastname: model.Lastname, Language: model.Language,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	status := fiber.StatusOK
	if result.IsNewUser {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(result)
}

// @Summary Facebook Data Deletion Callback
// @Description Called by Facebook when a user removes the app and asks for their data to be deleted. The form field signed_request is verified with the app secret; the linked account is deleted. Returns the status URL and confirmation code Facebook shows to the user.
// @Tags Auth
// @Accept x-www-form-urlencoded
// @Produce json
// @Param signed_request formData string true "Facebook signed request"
// @Success 200 {object} dtos.FacebookDataDeletionResponseModel
// @Failure 400 {object} map[string]interface{} "Facebook sign-in is not enabled"
// @Failure 401 {object} map[string]interface{} "Invalid signature"
// @Router /auth/facebook/data-deletion [post]
func (r *AuthController) FacebookDataDeletion(c *fiber.Ctx) error {
	signedRequest := c.FormValue("signed_request")
	if signedRequest == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.invalid_signed_request"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[dtos.ProviderDataDeletionResult](ctx, r.sender, authCommands.ProviderDataDeletionCommand{
		Provider: entities.ProviderFacebook, SignedRequest: signedRequest,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(dtos.FacebookDataDeletionResponseModel{
		URL:              c.BaseURL() + "/api/v1/auth/facebook/data-deletion/" + result.ConfirmationCode,
		ConfirmationCode: result.ConfirmationCode,
	})
}

// @Summary Data Deletion Status
// @Description Shows the status of a data deletion request by the confirmation code given to Facebook.
// @Tags Auth
// @Produce json
// @Param code path string true "Confirmation code"
// @Success 200 {object} dtos.DataDeletionStatusResponseModel
// @Failure 404 {object} map[string]interface{} "Unknown code"
// @Router /auth/facebook/data-deletion/{code} [get]
func (r *AuthController) DataDeletionStatus(c *fiber.Ctx) error {
	code := strings.ToLower(c.Params("code"))
	if len(code) != 32 || strings.Trim(code, "0123456789abcdef") != "" {
		return c.Status(fiber.StatusNotFound).JSON(helpers.LocalizedErrorMap(c, r.localizer, "auth.error.data_deletion_not_found"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	status, err := cqrs.Send[dtos.DataDeletionStatusResponseModel](ctx, r.sender, authQueries.GetDataDeletionStatusQuery{ConfirmationCode: code})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(status)
}

// @Summary Forgot Password
// @Description Generates a 6-character reset code for the given email and sends it to the user's email address.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body dtos.ForgotPasswordRequest true "Email address"
// @Success 200 {object} dtos.SuccessResponseModel "Operation status"
// @Failure 400 {object} dtos.SuccessResponseModel "Bad request"
// @Router /auth/forget [post]
func (r *AuthController) ForgotPassword(c *fiber.Ctx) error {

	model := new(dtos.ForgotPasswordRequest)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	_, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, authCommands.ForgotPasswordCommand{Email: model.Email})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}

	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}

// @Summary Reset Password
// @Description Verifies the 6-character reset code and updates the user's password.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body dtos.ResetPasswordRequest true "Reset credentials"
// @Success 200 {object} map[string]string "Password updated"
// @Failure 400 {object} map[string]interface{} "Bad request or invalid code"
// @Router /auth/reset [post]
func (r *AuthController) ResetPassword(c *fiber.Ctx) error {

	model := new(dtos.ResetPasswordRequest)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, authCommands.ResetPasswordCommand{
		Email: model.Email, Code: model.Code, NewPassword: model.NewPassword,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(helpers.LocalizedMessageMap(c, r.localizer, "auth.message.password_reset_successful"))
}

// @Summary Send Email Verification Code
// @Description Sends a 6-character verification code to the authenticated user's email address.
// @Tags Auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} dtos.SuccessResponseModel "Verification code sent"
// @Failure 400 {object} dtos.SuccessResponseModel "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /auth/validate-email [get]
func (r *AuthController) SendEmailVerificationCode(c *fiber.Ctx) error {

	email, ok := c.Locals("userEmail").(string)
	if !ok || email == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(dtos.SuccessResponseModel{Success: false})
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, authCommands.SendEmailVerificationCodeCommand{Email: email}); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}

	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}

// @Summary Validate Email
// @Description Verifies the authenticated user's email address with the submitted 6-character code.
// @Tags Auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body dtos.ValidateEmailRequest true "Verification code"
// @Success 200 {object} dtos.SuccessResponseModel "Email verified"
// @Failure 400 {object} dtos.SuccessResponseModel "Bad request or invalid code"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /auth/validate-email [post]
func (r *AuthController) ValidateEmail(c *fiber.Ctx) error {
	model := new(dtos.ValidateEmailRequest)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}

	email, ok := c.Locals("userEmail").(string)
	if !ok || email == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(dtos.SuccessResponseModel{Success: false})
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, authCommands.ValidateEmailCommand{
		Email: email, Code: model.Code,
	}); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(dtos.SuccessResponseModel{Success: false})
	}

	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}
