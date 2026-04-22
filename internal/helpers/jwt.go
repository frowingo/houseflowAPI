package helpers

import (
	"errors"
	"houseflowApi/internal/config"
	"houseflowApi/internal/models/dtos"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type customClaims struct {
	Role int `json:"role"`
	jwt.RegisteredClaims
}

func GenerateToken(email string, userId string, role int) (string, error) {

	config, err := config.LoadConfig()
	if err != nil {
		return "", errors.New("config not found")
	}

	claim := customClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    email,
			Subject:   userId,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(72 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claim)
	signedToken, err := token.SignedString([]byte(config.Internal.JWT.ApiSecret))
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

func ValidateToken(token string) (dtos.JwtModel, error) {

	config, err := config.LoadConfig()
	if err != nil {
		return dtos.JwtModel{}, errors.New("config not found")
	}

	parsedToken, err := jwt.ParseWithClaims(token, &customClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.Internal.JWT.ApiSecret), nil
	})
	if err != nil {
		return dtos.JwtModel{}, err
	}

	if claims, ok := parsedToken.Claims.(*customClaims); ok && parsedToken.Valid {
		return dtos.JwtModel{
			Issuer:     claims.Issuer,
			Subject:    claims.Subject,
			IssuerRole: claims.Role,
			ExpiresAt:  claims.ExpiresAt.Time,
			IssuedAt:   claims.IssuedAt.Time,
		}, nil
	} else {
		return dtos.JwtModel{}, errors.New("invalid token")
	}
}
