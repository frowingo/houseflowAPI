package handlers

import (
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type UserHandler struct {
	userService *services.UserService
}

// NewUserHandler constructor for UserHandler
func NewUserHandler(userService *services.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

// @Tags User
// @Accept json
// @Produce json
// @Param user body dtos.NewUserModel true "User object"
// @Success 201 {object} dtos.NewUserModel
// @Failure 400 {object} map[string]interface{}
// @Router /user [post]
func (r *UserHandler) NewUser(c *fiber.Ctx) error {

	user := new(dtos.NewUserModel)

	if err := c.BodyParser(user); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	_, err := r.userService.CreateUser(*user)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(201).JSON(user)

}
