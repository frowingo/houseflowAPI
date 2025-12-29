package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

type AuthService struct {
	dbRepository *abstract.DbRepository[entities.User]
}

func NewAuthService(dbRepository *abstract.DbRepository[entities.User]) *AuthService {
	return &AuthService{
		dbRepository: dbRepository,
	}
}

func (r *AuthService) Login(email string, password string) (string, error) {

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil {
		return "", err
	}

	if user != nil {
		isValid := helpers.CheckPasswordHash(password, user.HashPassword)
		if isValid {

			token, err := helpers.GenerateToken(user.Email, user.Id.String())
			if err != nil {
				return "", err
			}
			return token, nil
		}
	} else {
		return "", errors.New("user not found")
	}

	return "", nil
}
