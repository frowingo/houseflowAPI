package helpers

import (
	"houseflowApi/internal/models/dtos"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type customClaims struct {
	Role     int    `json:"role"`
	Language string `json:"language"`
	jwt.RegisteredClaims
}

type JWTService struct {
	apiSecret []byte
}

func NewJWTService(apiSecret string) *JWTService {
	return &JWTService{apiSecret: []byte(apiSecret)}
}

func (s *JWTService) GenerateToken(email string, userId string, role int, language string) (string, error) {
	claim := customClaims{
		Role:     role,
		Language: NormalizeLanguage(language),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    email,
			Subject:   userId,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(72 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claim)
	signedToken, err := token.SignedString(s.apiSecret)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

func (s *JWTService) ValidateToken(token string) (dtos.JwtModel, error) {
	parsedToken, err := jwt.ParseWithClaims(token, &customClaims{}, func(t *jwt.Token) (interface{}, error) {
		return s.apiSecret, nil
	})
	if err != nil {
		return dtos.JwtModel{}, err
	}

	if claims, ok := parsedToken.Claims.(*customClaims); ok && parsedToken.Valid {
		return dtos.JwtModel{
			Issuer:     claims.Issuer,
			Subject:    claims.Subject,
			IssuerRole: claims.Role,
			Language:   NormalizeLanguage(claims.Language),
			ExpiresAt:  dtos.NewUTCDateTime(claims.ExpiresAt.Time),
			IssuedAt:   dtos.NewUTCDateTime(claims.IssuedAt.Time),
		}, nil
	}
	return dtos.JwtModel{}, NewLocalizedError("auth.error.invalid_token")
}
