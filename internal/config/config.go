package config

import (
	"embed"
	"encoding/json"
	"errors"
	"os"
	"strconv"
)

var staticFiles embed.FS

func ReadStaticFile(name string) ([]byte, error) {
	return staticFiles.ReadFile("staticFiles/" + name)
}

type ConfigModel struct {
	External ConfigExternal `json:"external"`
	Internal ConfigInternal `json:"internal"`
}

type ConfigExternal struct {
	Mongo      ConfigMongo      `json:"mongo"`
	Cloudinary ConfigCloudinary `json:"cloudinary"`
}

type ConfigInternal struct {
	JWT           ConfigJWT           `json:"jwt"`
	PasswordReset ConfigPasswordReset `json:"passwordReset"`
	SMTP          ConfigSMTP          `json:"smtp"`
}

type ConfigPasswordReset struct {
	Secret          string `json:"secret"`
	ValidityMinutes int    `json:"validityMinutes"`
}

type ConfigJWT struct {
	ApiSecret string `json:"apiSecret"`
}

type ConfigSMTP struct {
	Password string `json:"password"`
}

type ConfigMongo struct {
	ConnectionString string `json:"connectionString"`
	DbName           string `json:"dbName"`
}

type ConfigCloudinary struct {
	CloudName string `json:"cloudName"`
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
	Folder    string `json:"folder"`
}

func isDebugMode() bool {
	return os.Getenv("APP_ENV") != "production"
}

func LoadConfig() (*ConfigModel, error) {
	configFilePath := "./internal/config/config.json"

	file, err := os.ReadFile(configFilePath)
	var config ConfigModel
	if err != nil && isDebugMode() {
		return nil, errors.New("failed to read config file:" + err.Error())
	}

	if err == nil {
		if err := json.Unmarshal(file, &config); err != nil {
			return nil, errors.New("config.json cannot deserialize:" + err.Error())
		}
	}

	applyEnvOverrides(&config)

	return &config, nil
}

func applyEnvOverrides(config *ConfigModel) {
	if uri := os.Getenv("MONGO_URI"); uri != "" {
		config.External.Mongo.ConnectionString = uri
	}

	if db := os.Getenv("MONGO_DB"); db != "" {
		config.External.Mongo.DbName = db
	}

	if cloudName := os.Getenv("CLOUDINARY_CLOUD_NAME"); cloudName != "" {
		config.External.Cloudinary.CloudName = cloudName
	}

	if apiKey := os.Getenv("CLOUDINARY_API_KEY"); apiKey != "" {
		config.External.Cloudinary.APIKey = apiKey
	}

	if apiSecret := os.Getenv("CLOUDINARY_API_SECRET"); apiSecret != "" {
		config.External.Cloudinary.APISecret = apiSecret
	}

	if folder := os.Getenv("CLOUDINARY_FOLDER"); folder != "" {
		config.External.Cloudinary.Folder = folder
	}

	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		config.Internal.JWT.ApiSecret = jwtSecret
	}

	if resetSecret := os.Getenv("RESET_CODE_SECRET"); resetSecret != "" {
		config.Internal.PasswordReset.Secret = resetSecret
	}

	if validity := os.Getenv("RESET_CODE_VALIDITY_MINUTES"); validity != "" {
		minutes, err := strconv.Atoi(validity)
		if err == nil && minutes > 0 {
			config.Internal.PasswordReset.ValidityMinutes = minutes
		}
	}

	if smtpPassword := os.Getenv("SMTP_PASSWORD"); smtpPassword != "" {
		config.Internal.SMTP.Password = smtpPassword
	}
}

func Validate(config *ConfigModel) error {
	if config.External.Mongo.ConnectionString == "" {
		return errors.New("mongo connection string is required")
	}
	if config.External.Mongo.DbName == "" {
		return errors.New("mongo db name is required")
	}
	if config.Internal.JWT.ApiSecret == "" {
		return errors.New("jwt secret is required")
	}
	if config.Internal.PasswordReset.Secret == "" {
		return errors.New("password reset secret is required")
	}
	if config.Internal.PasswordReset.ValidityMinutes <= 0 {
		return errors.New("password reset validity must be greater than zero")
	}
	return nil
}

func MustLoadConfig() (*ConfigModel, error) {
	config, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if err := Validate(config); err != nil {
		return nil, err
	}
	return config, nil
}
