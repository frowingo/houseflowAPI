package helpers

func HashPassword(password string) (string, error) {

	return password, nil
}

func CheckPasswordHash(password string, hash string) bool {

	return password == hash
}
