package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
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
	const MaxFailedAttempts = 10

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil && err.Error() == "document not found" {
		return "", errors.New("sen kimsin birader, böyle bi mail yok") //invalid email or password
	} else if err != nil {
		return "", err
	}

	// Check if account is locked
	if !user.IsActive {
		return "", errors.New("sen şimdi naneyi yimedin mi? BANLANDIN!") //account is locked due to multiple failed login attempts
	}

	isValid := helpers.CheckPasswordHash(password, user.HashPassword)
	if isValid {
		// Password correct: reset failed attempts and update last login
		token, err := helpers.GenerateToken(user.Email, user.Id.Hex(), int(user.Role))
		if err != nil {
			return "", err
		}

		_ = r.dbRepository.UpdateFields(user.Id, bson.M{
			"lastLogin":           time.Now(),
			"failedLoginAttempts": 0,
		})

		return token, nil
	}

	// Password incorrect: increment failed attempts
	user.FailedLoginAttempts++
	updateData := bson.M{"failedLoginAttempts": user.FailedLoginAttempts}

	// Lock account if max attempts reached
	if user.FailedLoginAttempts >= MaxFailedAttempts {
		updateData["isActive"] = false
		_ = r.dbRepository.UpdateFields(user.Id, updateData)
		return "", errors.New("sen şimdi naneyi yimedin mi? BANLANDIN!") //too many failed login attempts, account has been locked
	}

	_ = r.dbRepository.UpdateFields(user.Id, updateData)
	return "", errors.New("TEZGAHHH LAN BU, yanlış şifre!") //invalid email or password
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

	token, err := helpers.GenerateToken(entity.Email, entity.Id.Hex(), int(entity.Role))
	if err != nil {
		return "", err
	}

	return token, nil
}

func (r *AuthService) ForgotPassword(email string) (string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return "", err
	}

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil {

		if user == nil || err.Error() == "document not found" {
			return "", errors.New("user not found")
		}

		return "", err
	}

	window := helpers.ResetCodeWindow(cfg.Internal.PasswordReset.ValidityMinutes)
	code := helpers.GenerateResetCode(email, cfg.Internal.PasswordReset.Secret, window)
	return code, nil
}

func (r *AuthService) ResetPassword(email, code, newPassword string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	secret := cfg.Internal.PasswordReset.Secret
	validityMinutes := cfg.Internal.PasswordReset.ValidityMinutes

	currentWindow := helpers.ResetCodeWindow(validityMinutes)
	previousWindow := currentWindow.Add(-time.Duration(validityMinutes) * time.Minute)

	currentCode := helpers.GenerateResetCode(email, secret, currentWindow)
	previousCode := helpers.GenerateResetCode(email, secret, previousWindow)

	if code != currentCode && code != previousCode {
		return errors.New("invalid or expired reset code")
	}

	user, err := r.dbRepository.FindByColumn("email", email)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}

	hashedPassword, err := helpers.HashPassword(newPassword)
	if err != nil {
		return err
	}

	return r.dbRepository.UpdateFields(user.Id, bson.M{
		"password":            hashedPassword,
		"isActive":            true,
		"failedLoginAttempts": 0,
		"updatedOn":           time.Now(),
	})
}
