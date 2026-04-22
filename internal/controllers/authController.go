package controllers

import (
	"houseflowApi/external/validator"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type AuthController struct {
	authService *services.AuthService
	validator   *validator.CustomValidator
}

func NewAuthController(authService *services.AuthService) *AuthController {
	return &AuthController{
		authService: authService,
		validator:   validator.NewValidator(),
	}
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	if model.Email == "" || model.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email and password are required",
		})
	}

	token, err := r.authService.Login(model.Email, model.Password)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if token == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid credentials",
		})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	token, err := r.authService.SignUp(*model)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(201).JSON(fiber.Map{"token": token})
}

// @Summary Forgot Password
// @Description Generates a 6-character reset code for the given email. In production the code should be delivered via email; here it is returned in the response body.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body dtos.ForgotPasswordRequest true "Email address"
// @Success 200 {object} map[string]string "Reset code (deliver via email in production)"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /auth/forget [post]
func (r *AuthController) ForgotPassword(c *fiber.Ctx) error {
	model := new(dtos.ForgotPasswordRequest)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	code, err := r.authService.ForgotPassword(model.Email)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	//TODO: mail code to user
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "success",
		"code":    code,
	})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if err := r.authService.ResetPassword(model.Email, model.Code, model.NewPassword); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "Password reset successful",
	})
}
