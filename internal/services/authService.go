package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
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

func (r *AuthService) SignUp(model dtos.SignUpUserModel) (string, error) {

	user, err := r.dbRepository.FindByColumn("email", model.Email)

	// user email must unique
	if user != nil {
		return "", errors.New("user already exists")
	} else {
		if err != nil && err.Error() != "document not found" {
			return "", err
		}
	}

	hashedPassword, err := helpers.HashPassword(model.Password)
	if err != nil {
		return "", err
	}

	model.Password = hashedPassword
	entity := model.ToEntity()

	_, err = r.dbRepository.Insert(entity)
	if err != nil {
		return "", err
	}

	token, err := helpers.GenerateToken(entity.Email, entity.Id.String())
	if err != nil {
		return "", err
	}

	return token, nil
}
