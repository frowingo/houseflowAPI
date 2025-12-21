package config

import (
	"encoding/json"
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
	configFilePath := "internal/config/config.json"

	file, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	var config ConfigModel
	if err := json.Unmarshal(file, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
