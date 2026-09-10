package commands

import (
	"context"
	"time"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const maxFailedLoginAttempts = 10

type LoginCommand struct {
	cqrs.Request[string]
	Email    string
	Password string
}

type LoginHandler struct {
	userRepository *abstract.DbRepository[entities.User]
}

func NewLoginHandler(userRepository *abstract.DbRepository[entities.User]) *LoginHandler {
	return &LoginHandler{userRepository: userRepository}
}

func (h *LoginHandler) Handle(ctx context.Context, command LoginCommand) (string, error) {
	user, err := h.userRepository.FindByColumn(ctx, "email", command.Email)
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		return "", helpers.NewLocalizedError("auth.error.email_not_found")
	}
	if err != nil {
		return "", err
	}
	if !user.IsActive {
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}

	if helpers.CheckPasswordHash(command.Password, user.HashPassword) {
		result, err := h.userRepository.Collection().UpdateOne(ctx, bson.M{
			"_id": user.Id, "isActive": true,
		}, bson.M{"$set": bson.M{
			"lastLogin": time.Now(), "failedLoginAttempts": 0,
		}})
		if err != nil {
			return "", err
		}
		if result.MatchedCount == 0 {
			return "", helpers.NewLocalizedError("auth.error.account_locked")
		}
		return helpers.GenerateToken(user.Email, user.Id.Hex(), int(user.Role), user.Language)
	}

	var updatedUser entities.User
	err = h.userRepository.Collection().FindOneAndUpdate(ctx,
		bson.M{"_id": user.Id, "isActive": true},
		mongo.Pipeline{
			{{Key: "$set", Value: bson.M{
				"failedLoginAttempts": bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$failedLoginAttempts", 0}}, 1}},
			}}},
			{{Key: "$set", Value: bson.M{
				"isActive": bson.M{"$cond": bson.A{
					bson.M{"$gte": bson.A{"$failedLoginAttempts", maxFailedLoginAttempts}}, false, "$isActive",
				}},
			}}},
		}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedUser)
	if err == mongo.ErrNoDocuments {
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}
	if err != nil {
		return "", err
	}
	if !updatedUser.IsActive {
		return "", helpers.NewLocalizedError("auth.error.account_locked")
	}
	return "", helpers.NewLocalizedError("auth.error.invalid_password")
}

var _ cqrs.CommandHandler[LoginCommand, string] = (*LoginHandler)(nil)
