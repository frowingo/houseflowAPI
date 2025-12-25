package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
)

type AuthService struct {
	dbRepository *abstract.DbRepository[entities.User]
}

func NewAuthService(dbRepository *abstract.DbRepository[entities.User]) *AuthService {
	return &AuthService{
		dbRepository: dbRepository,
	}
}

func (r *AuthService) SignIn(email string, password string) (*entities.User, error) {
	return nil, nil
}
