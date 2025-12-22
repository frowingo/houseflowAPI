package config

import (
	"encoding/json"
	"errors"
	"os"
)

type ConfigModel struct {
	External ConfigExternal `json:"external"`
}

type ConfigExternal struct {
	AppWrite ConfigAppWrite `json:"appwrite"`
	Mongo    ConfigMongo    `json:"mongo"`
}

type ConfigAppWrite struct {
	Endpoint   string `json:"endpoint"`
	ProjectId  string `json:"project_id"`
	DatabaseId string `json:"database_id"`
	ApiKey     string `json:"apiKey"`
	ApiSecret  string `json:"apiSecret"`
}

type ConfigMongo struct {
	DevConString  string `json:"devConString"`
	ProdConString string `json:"prodConString"`
	DbName        string `json:"dbName"`
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

	return &config, nil
}
