package services

import (
	"context"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var imageAssetCache = helpers.NewInMemoryCache[[]dtos.ImageAssetResultModel]()

type UserService struct {
	dbRepository              *abstract.DbRepository[entities.User]
	houseRepository           *abstract.DbRepository[entities.House]
	imageAssetRepository      *abstract.DbRepository[entities.ImageAsset]
	userInfoHistoryRepository *abstract.DbRepository[entities.UserInfoHistory]
}

func NewUserService(
	dbRepository *abstract.DbRepository[entities.User],
	houseRepository *abstract.DbRepository[entities.House],
	imageAssetRepository *abstract.DbRepository[entities.ImageAsset],
	userInfoHistoryRepositories ...*abstract.DbRepository[entities.UserInfoHistory],
) *UserService {
	var userInfoHistoryRepository *abstract.DbRepository[entities.UserInfoHistory]
	if len(userInfoHistoryRepositories) > 0 {
		userInfoHistoryRepository = userInfoHistoryRepositories[0]
	}

	return &UserService{
		dbRepository:              dbRepository,
		houseRepository:           houseRepository,
		imageAssetRepository:      imageAssetRepository,
		userInfoHistoryRepository: userInfoHistoryRepository,
	}
}

func (r *UserService) CreateUser(ctx context.Context, user dtos.NewUserModel) (*dtos.NewUserModel, error) {

	entity := user.ToEntity()
	entity.Language = helpers.NormalizeLanguage(entity.Language)

	hashedPassword, err := helpers.HashPassword(user.Password)
	if err != nil {
		return nil, err
	}
	entity.HashPassword = hashedPassword

	var createdUser *entities.User
	err = r.dbRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		createdUser, err = r.dbRepository.Insert(txCtx, entity)
		if err != nil {
			return err
		}
		return insertUserInfoHistory(txCtx, r.userInfoHistoryRepository,
			newUserInfoHistoryEntries(*createdUser, createdUser.CreatedOn))
	})
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *UserService) GetUserByEmail(ctx context.Context, email string) (*dtos.UserResultModel, error) {

	user, err := r.dbRepository.FindByColumn(ctx, "email", email)
	if err != nil {
		return nil, err
	}

	result := dtos.UserToResultModel(*user)
	return &result, nil
}

func (r *UserService) ListByUsers(ctx context.Context) ([]dtos.UserResultModel, error) {

	users, err := r.dbRepository.FindAll(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]dtos.UserResultModel, 0, len(users))
	for _, user := range users {
		results = append(results, dtos.UserToResultModel(user))
	}

	return results, nil
}

func (r *UserService) DeleteUser(ctx context.Context, userId string) error {

	objectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return err
	}

	err = r.dbRepository.Delete(ctx, objectId)
	if err != nil {
		return err
	}

	return nil
}

func (r *UserService) GetUsersByHouse(ctx context.Context, houseId string, requesterId string) ([]dtos.UserResultModel, error) {
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, err
	}

	house, err := r.houseRepository.FindByID(ctx, houseObjectId)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(house.MemberIds, requesterId) {
		return nil, helpers.NewLocalizedError("house.error.user_not_member")
	}

	users := make([]dtos.UserResultModel, 0, len(house.MemberIds))
	for _, memberId := range house.MemberIds {
		userObjectId, err := helpers.ToMongoId(memberId)
		if err != nil {
			continue
		}
		user, err := r.dbRepository.FindByID(ctx, userObjectId)
		if err != nil {
			continue
		}
		users = append(users, dtos.UserToResultModel(*user))
	}

	return users, nil
}

func (r *UserService) UpdateProfile(ctx context.Context, userId string, model dtos.UpdateUserModel) (*dtos.UserResultModel, error) {

	objectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	historyChanges := profileUserInfoChanges(model)
	fields := bson.M{"updatedOn": now}

	if model.Firstname != nil {
		fields["firstName"] = *model.Firstname
	}
	if model.Lastname != nil {
		fields["lastName"] = *model.Lastname
	}
	if model.PhoneNumber != nil {
		fields["phoneNumber"] = *model.PhoneNumber
	}
	if model.BirthDay != nil {
		fields["birthDay"] = model.BirthDay.Time
	}
	if model.ImageURL != nil {
		fields["imageUrl"] = *model.ImageURL
	}
	if model.Language != nil {
		if !helpers.IsSupportedLanguage(*model.Language) {
			return nil, helpers.NewLocalizedError("localization.error.unsupported_language")
		}
		fields["language"] = helpers.NormalizeLanguage(*model.Language)
	}
	var updated *entities.User
	err = r.dbRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if err := validateProfileUpdateIntervals(txCtx, r.userInfoHistoryRepository, userId, historyChanges, now); err != nil {
			return err
		}
		if err := r.dbRepository.UpdateFields(txCtx, objectId, fields); err != nil {
			return err
		}
		if err := insertUserInfoHistory(txCtx, r.userInfoHistoryRepository,
			userInfoHistoryEntries(userId, historyChanges, now)); err != nil {
			return err
		}
		updated, err = r.dbRepository.FindByID(txCtx, objectId)
		return err
	})
	if err != nil {
		return nil, err
	}

	result := dtos.UserToResultModel(*updated)

	return &result, nil
}

func (r *UserService) GetImagesByCategory(ctx context.Context, category string) ([]dtos.ImageAssetResultModel, error) {

	if cached, ok := imageAssetCache.Get("images_" + category); ok {
		return cached, nil
	}

	assets, err := r.imageAssetRepository.FindManyByFilter(ctx, bson.M{"category": category, "isActive": true})
	if err != nil {
		return nil, err
	}

	results := make([]dtos.ImageAssetResultModel, 0, len(assets))
	for _, a := range assets {
		results = append(results, dtos.ToImageAssetResultModel(a))
	}

	if len(results) > 0 {
		imageAssetCache.Set("images_"+category, results)
	}

	return results, nil
}

func (r *UserService) GetImageByPublicID(ctx context.Context, publicId string) (*dtos.ImageAssetResultModel, error) {

	asset, err := r.imageAssetRepository.FindByColumn(ctx, "publicId", publicId)
	if err != nil {
		return nil, err
	}
	if !asset.IsActive {
		return nil, helpers.NewLocalizedError("image_asset.error.not_found")
	}

	result := dtos.ToImageAssetResultModel(*asset)

	return &result, nil
}

func (r *UserService) UpdateImageAsset(ctx context.Context, model dtos.UpdateImageAssetModel) error {

	asset, err := r.imageAssetRepository.FindByColumn(ctx, "publicId", model.PublicID)
	if err != nil {
		return helpers.NewLocalizedError("image_asset.error.not_found")
	}

	fields := bson.M{"updatedOn": time.Now()}

	if model.FileURL != nil {
		fields["fileUrl"] = *model.FileURL
	}
	if model.IsActive != nil {
		fields["isActive"] = *model.IsActive
	}

	if err := r.imageAssetRepository.UpdateFields(ctx, asset.Id, fields); err != nil {
		return err
	}

	imageAssetCache.Delete("images_" + asset.Category)

	return nil
}

func (r *UserService) CreateImageAsset(ctx context.Context, model dtos.CreateImageAssetModel) error {
	entity := model.ToEntity()

	exists, err := r.imageAssetRepository.ExistsByFilter(ctx, bson.M{"publicId": entity.PublicID})
	if err != nil {
		return err
	}
	if exists {
		return helpers.NewConflictError("image_asset.error.duplicate")
	}

	_, err = r.imageAssetRepository.Insert(ctx, entity)
	if mongo.IsDuplicateKeyError(err) {
		return helpers.NewConflictError("image_asset.error.duplicate")
	}
	return err
}
