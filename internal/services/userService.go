package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
)

type UserService struct {
	dbRepository *abstract.DbRepository[entities.User]
}

func (r *UserService) CreateUser(user entities.User) (*entities.User, error) {

	result, err := r.dbRepository.Insert(user)
	if err != nil {
		return nil, err
	}

	return result, nil
}
