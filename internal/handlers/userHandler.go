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

// @Summary List all users
// @Description Get a list of all users in the system
// @Tags User
// @Accept json
// @Produce json
// @Success 200 {array} dtos.NewUserModel
// @Failure 400 {object} map[string]interface{}
// @Router /user/usersList [get]
func (r *UserHandler) ListUsers(c *fiber.Ctx) error {

	users, err := r.userService.ListByUsers()
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(200).JSON(users)
}

// @Summary Delete a user
// @Description Delete a user by their ID
// @Tags User
// @Accept json
// @Produce json
// @Param id path string true "User ID"
// @Success 204
// @Failure 400 {object} map[string]interface{}
// @Router /user/{id} [delete]
func (r *UserHandler) DeleteUser(c *fiber.Ctx) error {

	userId := c.Params("id")

	err := r.userService.DeleteUser(userId)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.SendStatus(204)
}

// @Summary Get user by email
// @Description Retrieve a user by their email address
// @Tags User
// @Accept json
// @Produce json
// @Param email query string true "User email"
// @Success 200 {object} entities.User
// @Failure 400 {object} map[string]interface{}
// @Router /user/getByEmail [get]
func (r *UserHandler) GetUserByEmail(c *fiber.Ctx) error {

	email := c.Query("email")
	if email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email query param is required",
		})
	}

	user, err := r.userService.GetUserByEmail(email)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(200).JSON(user)
}
