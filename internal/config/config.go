package config

import (
	"encoding/json"
	"errors"
	"os"
)

type ConfigModel struct {
	External ConfigExternal `json:"external"`
	Internal ConfigInternal `json:"internal"`
}

type ConfigExternal struct {
	Mongo ConfigMongo `json:"mongo"`
}

type ConfigInternal struct {
	JWT           ConfigJWT           `json:"jwt"`
	PasswordReset ConfigPasswordReset `json:"passwordReset"`
}

type ConfigPasswordReset struct {
	Secret          string `json:"secret"`
	ValidityMinutes int    `json:"validityMinutes"`
}

type ConfigJWT struct {
	ApiSecret string `json:"apiSecret"`
}

type ConfigMongo struct {
	ConnectionString string `json:"connectionString"`
	DbName           string `json:"dbName"`
}

func isDebugMode() bool {
	return os.Getenv("APP_ENV") != "production"
}

func LoadConfig() (*ConfigModel, error) {
	configFilePath := "./internal/config/config.json"

	file, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, errors.New("failed to read config file:" + err.Error())
	}

	var config ConfigModel
	if err := json.Unmarshal(file, &config); err != nil {
		return nil, errors.New("config.json cannot deserialize:" + err.Error())
	}

	if isDebugMode() == false {

		if uri := os.Getenv("MONGO_URI"); uri != "" {
			config.External.Mongo.ConnectionString = uri
		}

		if db := os.Getenv("MONGO_DB"); db != "" {
			config.External.Mongo.DbName = db
		}

		if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
			config.Internal.JWT.ApiSecret = jwtSecret
		}

		if resetSecret := os.Getenv("RESET_CODE_SECRET"); resetSecret != "" {
			config.Internal.PasswordReset.Secret = resetSecret
		}
	}

	return &config, nil
}
