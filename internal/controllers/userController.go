package controllers

import (
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type UserController struct {
	userService *services.UserService
}

// NewUserController constructor for UserController
func NewUserController(userService *services.UserService) *UserController {
	return &UserController{
		userService: userService,
	}
}

// @Summary Create new user
// @Description Create a new user in the system
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param user body dtos.NewUserModel true "User object"
// @Success 201 {object} dtos.NewUserModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /user [post]
func (r *UserController) NewUser(c *fiber.Ctx) error {

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
// @Security BearerAuth
// @Success 200 {array} dtos.NewUserModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /user/usersList [get]
func (r *UserController) ListUsers(c *fiber.Ctx) error {

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
// @Security BearerAuth
// @Param id path string true "User ID"
// @Success 204
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /user/{id} [delete]
func (r *UserController) DeleteUser(c *fiber.Ctx) error {

	userId := c.Params("id")

	if userId == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "User ID is required",
		})
	}

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
// @Security BearerAuth
// @Param email query string true "User email"
// @Success 200 {object} entities.User
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /user/getByEmail [get]
func (r *UserController) GetUserByEmail(c *fiber.Ctx) error {

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
