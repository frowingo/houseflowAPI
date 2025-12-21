package database

import (
	"houseflowApi/internal/config"

	"github.com/appwrite/sdk-for-go/client"
	"github.com/appwrite/sdk-for-go/databases"
)

type AppwriteDbContext struct {
	Client   client.Client
	Database *databases.Databases
}

func NewAppwriteDbContext() (*AppwriteDbContext, error) {

	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}

	c := client.New(
		func(c *client.Client) error {
			c.Endpoint = cfg.External.AppWrite.Endpoint
			return nil
		},
		func(c *client.Client) error {
			c.AddHeader("X-Appwrite-Project", cfg.External.AppWrite.ProjectId)
			return nil
		},
		func(c *client.Client) error {
			c.AddHeader("X-Appwrite-Key", cfg.External.AppWrite.ApiSecret)
			return nil
		},
	)

	databes := databases.New(c)

	return &AppwriteDbContext{
		Client:   c,
		Database: databes,
	}, nil
}
