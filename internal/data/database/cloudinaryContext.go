package database

import (
	"context"
	"errors"
	"houseflowApi/internal/config"
	"io"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
)

type CloudinaryContext struct {
	client *cloudinary.Cloudinary
	folder string
}

func NewCloudinaryContext() (*CloudinaryContext, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}

	cloudCfg := cfg.External.Cloudinary
	if cloudCfg.CloudName == "" || cloudCfg.APIKey == "" || cloudCfg.APISecret == "" {
		return nil, errors.New("cloudinary config is missing required fields")
	}

	client, err := cloudinary.NewFromParams(cloudCfg.CloudName, cloudCfg.APIKey, cloudCfg.APISecret)
	if err != nil {
		return nil, errors.New("failed to create cloudinary client: " + err.Error())
	}

	return &CloudinaryContext{
		client: client,
		folder: cloudCfg.Folder,
	}, nil
}

func (r *CloudinaryContext) UploadImage(ctx context.Context, file io.Reader, publicID string) (string, error) {
	params := uploader.UploadParams{
		ResourceType: "image",
	}

	if r.folder != "" {
		params.Folder = r.folder
	}

	if publicID != "" {
		params.PublicID = publicID
		params.Overwrite = api.Bool(true)
	}

	result, err := r.client.Upload.Upload(ctx, file, params)
	if err != nil {
		return "", err
	}

	return result.SecureURL, nil
}
