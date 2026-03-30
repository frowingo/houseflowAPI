package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

type UserService struct {
	dbRepository    *abstract.DbRepository[entities.User]
	houseRepository *abstract.DbRepository[entities.House]
}

func NewUserService(
	dbRepository *abstract.DbRepository[entities.User],
	houseRepository *abstract.DbRepository[entities.House],
) *UserService {
	return &UserService{
		dbRepository:    dbRepository,
		houseRepository: houseRepository,
	}
}

func (r *UserService) CreateUser(user dtos.NewUserModel) (*dtos.NewUserModel, error) {

	entity := user.ToEntity()

	hashedPassword, err := helpers.HashPassword(user.Password)
	if err != nil {
		return nil, err
	}
	entity.HashPassword = hashedPassword

	_, err = r.dbRepository.Insert(entity)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *UserService) GetUserByEmail(email string) (*entities.User, error) {

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (r *UserService) ListByUsers() ([]entities.User, error) {

	users, err := r.dbRepository.FindAll()
	if err != nil {
		return nil, err
	}

	return users, nil
}

func (r *UserService) DeleteUser(userId string) error {

	objectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return err
	}

	err = r.dbRepository.Delete(objectId)
	if err != nil {
		return err
	}

	return nil
}

func (r *UserService) GetUsersByHouse(houseId string) ([]entities.User, error) {
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, err
	}

	house, err := r.houseRepository.FindById(houseObjectId)
	if err != nil {
		return nil, err
	}

	users := make([]entities.User, 0, len(house.MemberIds))
	for _, memberId := range house.MemberIds {
		userObjectId, err := helpers.ToMongoId(memberId)
		if err != nil {
			continue
		}
		user, err := r.dbRepository.FindById(userObjectId)
		if err != nil {
			continue
		}
		users = append(users, *user)
	}

	return users, nil
}

func (r *UserService) UpdateProfile(userId string, model dtos.UpdateUserModel) (*entities.User, error) {
	objectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return nil, err
	}

	fields := bson.M{"updatedOn": time.Now()}

	if model.Firstname != nil {
		fields["firstName"] = *model.Firstname
	}
	if model.Lastname != nil {
		fields["lastName"] = *model.Lastname
	}
	if model.PhoneNumber != nil {
		fields["phoneNumber"] = *model.PhoneNumber
	}
	if model.Age != nil {
		fields["age"] = *model.Age
	}
	if model.ImageURL != nil {
		fields["imageUrl"] = *model.ImageURL
	}
	if model.IsVerifyPhone != nil {
		fields["isVerifyPhone"] = *model.IsVerifyPhone
	}
	if model.IsVerifyEmail != nil {
		fields["isVerifyEmail"] = *model.IsVerifyEmail
	}

	if err := r.dbRepository.UpdateFields(objectId, fields); err != nil {
		return nil, err
	}

	updated, err := r.dbRepository.FindById(objectId)
	if err != nil {
		return nil, err
	}

	return updated, nil
}
