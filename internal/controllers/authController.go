package controllers

import (
	"houseflowApi/external/validator"
	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
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
