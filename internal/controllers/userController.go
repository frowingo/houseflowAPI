package controllers

import (
	"time"

	"houseflowApi/external/validator"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
)

type UserController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

// NewUserController constructor for UserController
func NewUserController(sender cqrs.Sender, localizer helpers.MessageLocalizer) *UserController {
	return &UserController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
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
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if err := r.validator.Validate(user); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	createdUser, err := cqrs.Send[*dtos.NewUserModel](ctx, r.sender, userCommands.CreateUserCommand{
		Firstname: user.Firstname, Lastname: user.Lastname, PhoneNumber: user.PhoneNumber,
		Email: user.Email, Password: user.Password, BirthDay: user.BirthDay.Time, Language: user.Language,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(201).JSON(createdUser)

}

// @Summary List all users
// @Description Get a list of all users in the system
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {array} dtos.UserResultModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /user/usersList [get]
func (r *UserController) ListUsers(c *fiber.Ctx) error {

	ctx, cancel := requestContext(c)
	defer cancel()
	users, err := cqrs.Send[[]dtos.UserResultModel](ctx, r.sender, userQueries.ListUsersQuery{})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
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
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "user.error.user_id_required"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	_, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, userCommands.DeleteUserCommand{UserID: userId})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
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
// @Success 200 {object} dtos.UserResultModel
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /user/getByEmail [get]
func (r *UserController) GetUserByEmail(c *fiber.Ctx) error {

	email := c.Query("email")
	if email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "user.error.email_query_required"))
	}
	userEmail := c.Locals("userEmail").(string)
	userRole := c.Locals("userRole").(int)
	if email != userEmail && userRole != int(entities.SuperAdmin) {
		return c.Status(fiber.StatusForbidden).JSON(helpers.LocalizedCoreError(c, r.localizer, "auth.error.forbidden"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	user, err := cqrs.Send[*dtos.UserResultModel](ctx, r.sender, userQueries.GetUserByEmailQuery{Email: email})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(200).JSON(user)
}

// @Summary Get users by house
// @Description Get all members of a house with their full details
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Success 200 {array} dtos.UserResultModel
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /user/getUsersByHouse [get]
func (r *UserController) GetUsersByHouse(c *fiber.Ctx) error {

	houseId := c.Query("houseId")
	if houseId == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "house.error.house_id_query_required"))
	}

	userId := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	users, err := cqrs.Send[[]dtos.UserResultModel](ctx, r.sender, userQueries.GetUsersByHouseQuery{
		HouseID: houseId, RequesterID: userId,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(200).JSON(users)
}

// @Summary Update user profile
// @Description Update user profile fields. Only provided (non-null) fields will be updated.
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param user body dtos.UpdateUserModel true "Fields to update"
// @Success 200 {object} core.ApiResponse[dtos.UserResultModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse
// @Router /user/profile/{id} [put]
func (r *UserController) UpdateProfile(c *fiber.Ctx) error {

	model := new(dtos.UpdateUserModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}

	userId := c.Locals("userID").(string)

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	var birthDay *time.Time
	if model.BirthDay != nil {
		value := model.BirthDay.Time
		birthDay = &value
	}
	updated, err := cqrs.Send[*dtos.UserResultModel](ctx, r.sender, userCommands.UpdateProfileCommand{
		UserID: userId, Firstname: model.Firstname, Lastname: model.Lastname,
		PhoneNumber: model.PhoneNumber, BirthDay: birthDay, ImageURL: model.ImageURL, Language: model.Language,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(200).JSON(core.Success(updated))
}

// @Summary Get images by category
// @Description Retrieve all image assets for a given category
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param category query string true "Image category"
// @Success 200 {object} core.ApiResponse[[]dtos.ImageAssetResultModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /user/getImages [get]
func (r *UserController) GetImages(c *fiber.Ctx) error {
	category := c.Query("category")
	if category == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "image_asset.error.category_query_required"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	images, err := cqrs.Send[[]dtos.ImageAssetResultModel](ctx, r.sender, imageAssetQueries.GetImagesByCategoryQuery{
		Category: category,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(200).JSON(core.Success(images))
}

// @Summary Get image by public ID
// @Description Retrieve an image asset by its public ID
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param publicId query string true "Image public ID"
// @Success 200 {object} core.ApiResponse[[]dtos.ImageAssetResultModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /user/getImage [get]
func (r *UserController) GetImage(c *fiber.Ctx) error {
	publicId := c.Query("publicId")
	if publicId == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "image_asset.error.public_id_query_required"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	image, err := cqrs.Send[*dtos.ImageAssetResultModel](ctx, r.sender, imageAssetQueries.GetImageByPublicIDQuery{
		PublicID: publicId,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(200).JSON(core.Success([]dtos.ImageAssetResultModel{*image}))
}

// @Summary Update image asset
// @Description Update fileURL and/or isActive of an image asset by publicId (SuperAdmin only)
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param image body dtos.UpdateImageAssetModel true "Fields to update"
// @Success 200 {object} core.ApiResponse[any]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Forbidden"
// @Router /user/images [put]
func (r *UserController) UpdateImageAsset(c *fiber.Ctx) error {
	model := new(dtos.UpdateImageAssetModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, imageAssetCommands.UpdateImageAssetCommand{
		PublicID: model.PublicID, FileURL: model.FileURL, IsActive: model.IsActive,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(200).JSON(core.Success[any](nil))
}

// @Summary Create image asset
// @Description Create a new image asset (SuperAdmin only)
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param image body dtos.CreateImageAssetModel true "Image asset data"
// @Success 201 {object} core.ApiResponse[any]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Forbidden"
// @Router /user/images [post]
func (r *UserController) CreateImageAsset(c *fiber.Ctx) error {
	model := new(dtos.CreateImageAssetModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, imageAssetCommands.CreateImageAssetCommand{
		Category: model.Category, FileName: model.FileName, FileURL: model.FileURL, IsActive: model.IsActive,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(201).JSON(core.Success[any](nil))
}
