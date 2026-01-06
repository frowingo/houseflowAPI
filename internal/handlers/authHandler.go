package handlers

import (
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type AuthHandler struct {
	authService *services.AuthService
}

func NewAuthHandler(authService *services.AuthService) *AuthHandler {
	return &AuthHandler{
		authService: authService,
	}
}

// @Summary User Login
// @Description Login with email and password to receive JWT token
// @Tags Auth
// @Accept json
// @Produce json
// @Param email path string true "User email address"
// @Param password path string true "User password"
// @Success 200 {object} map[string]string "Returns JWT token"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Invalid credentials"
// @Router /auth/login/{email}/{password} [get]
func (r *AuthHandler) Login(c *fiber.Ctx) error {

	email := c.Params("email")
	password := c.Params("password")

	if email == "" || password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email and password are required",
		})
	}

	token, err := r.authService.Login(email, password)
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
// @Description Signup with email and password to receive JWT token
// @Tags Auth
// @Accept json
// @Produce json
// @Param email path string true "User email address"
// @Param password path string true "User password"
// @Success 200 {object} map[string]string "Returns JWT token"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Invalid credentials"
// @Router /auth/signup/{email}/{password} [get]
func (r *AuthHandler) Signup(c *fiber.Ctx) error {

	email := c.Params("email")
	password := c.Params("password")

	if email == "" || password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email and password are required",
		})
	}

	token, err := r.authService.SignUp(email, password)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(201).JSON(fiber.Map{"token": token})
}
