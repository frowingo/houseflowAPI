package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
)

type UserService struct {
	dbRepository *abstract.DbRepository[entities.User]
}

func NewUserService(dbRepository *abstract.DbRepository[entities.User]) *UserService {
	return &UserService{
		dbRepository: dbRepository,
	}
}

func (r *UserService) CreateUser(user dtos.NewUserModel) (*dtos.NewUserModel, error) {

	entity := user.ToEntity()

	_, err := r.dbRepository.Insert(entity)
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
