package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/models/dtos"
)

type UserService struct {
	dbRepository *abstract.DbRepository[entities.User]
}

// NewUserService constructor for UserService
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
