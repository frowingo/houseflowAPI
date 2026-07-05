package helpers

import "golang.org/x/crypto/bcrypt"

func HashPassword(password string) (string, error) {

	hashedPass, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", NewLocalizedError("auth.error.hash_password_failed", err.Error())
	}

	return string(hashedPass), nil
}

func CheckPasswordHash(password string, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
