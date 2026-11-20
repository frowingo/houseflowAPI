package commands

import (
	"context"
	"strings"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const maxDeviceTokenLength = 4096

// RegisterDeviceCommand stores the FCM token of the signed-in device. A
// token already registered by another account moves to this user.
type RegisterDeviceCommand struct {
	cqrs.Request[cqrs.NoResult]
	UserID   string
	Token    string
	Platform string
}

type RegisterDeviceHandler struct {
	deviceTokens databaseAbstract.DbRepository[entities.DeviceToken]
}

func NewRegisterDeviceHandler(deviceTokens databaseAbstract.DbRepository[entities.DeviceToken]) *RegisterDeviceHandler {
	return &RegisterDeviceHandler{deviceTokens: deviceTokens}
}

func (h *RegisterDeviceHandler) Handle(ctx context.Context, command RegisterDeviceCommand) (cqrs.NoResult, error) {
	token := strings.TrimSpace(command.Token)
	if token == "" || len(token) > maxDeviceTokenLength {
		return cqrs.NoResult{}, helpers.NewLocalizedError("notification.error.invalid_device_token")
	}
	platform := entities.DevicePlatform(command.Platform)
	if platform != entities.IOSDevice && platform != entities.AndroidDevice {
		return cqrs.NoResult{}, helpers.NewLocalizedError("notification.error.invalid_platform")
	}

	now := time.Now().UTC()
	update := func() error {
		_, err := h.deviceTokens.Collection().UpdateOne(ctx, bson.M{"token": token}, bson.M{
			"$set":         bson.M{"userId": command.UserID, "platform": platform, "updatedOn": now},
			"$setOnInsert": bson.M{"createdOn": now},
		}, options.Update().SetUpsert(true))
		return err
	}
	err := update()
	if mongo.IsDuplicateKeyError(err) {
		// A concurrent registration inserted the token first; update it.
		err = update()
	}
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("notification.error.failed_register_device")
	}
	return cqrs.NoResult{}, nil
}

// DeleteDeviceCommand removes the token of the signed-in device, for
// example on sign out.
type DeleteDeviceCommand struct {
	cqrs.Request[cqrs.NoResult]
	UserID string
	Token  string
}

type DeleteDeviceHandler struct {
	deviceTokens databaseAbstract.DbRepository[entities.DeviceToken]
}

func NewDeleteDeviceHandler(deviceTokens databaseAbstract.DbRepository[entities.DeviceToken]) *DeleteDeviceHandler {
	return &DeleteDeviceHandler{deviceTokens: deviceTokens}
}

func (h *DeleteDeviceHandler) Handle(ctx context.Context, command DeleteDeviceCommand) (cqrs.NoResult, error) {
	_, err := h.deviceTokens.Collection().DeleteOne(ctx, bson.M{
		"token": strings.TrimSpace(command.Token), "userId": command.UserID,
	})
	return cqrs.NoResult{}, err
}

var (
	_ cqrs.CommandHandler[RegisterDeviceCommand, cqrs.NoResult] = (*RegisterDeviceHandler)(nil)
	_ cqrs.CommandHandler[DeleteDeviceCommand, cqrs.NoResult]   = (*DeleteDeviceHandler)(nil)
)
