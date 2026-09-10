package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const profileFieldUpdateInterval = 20 * 24 * time.Hour

type UpdateProfileCommand struct {
	cqrs.Request[*dtos.UserResultModel]
	UserID      string
	Firstname   *string
	Lastname    *string
	PhoneNumber *string
	BirthDay    *time.Time
	ImageURL    *string
	Language    *string
}

type profileChange struct {
	columnName string
	value      any
	restricted bool
}

type UpdateProfileHandler struct {
	userRepository        databaseAbstract.DbRepository[entities.User]
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory]
}

func NewUpdateProfileHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory],
) *UpdateProfileHandler {
	return &UpdateProfileHandler{
		userRepository:        userRepository,
		userHistoryRepository: userHistoryRepository,
	}
}

func (h *UpdateProfileHandler) Handle(ctx context.Context, command UpdateProfileCommand) (*dtos.UserResultModel, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	changes := profileChanges(command)
	fields := bson.M{"updatedOn": now}
	if command.Firstname != nil {
		fields["firstName"] = *command.Firstname
	}
	if command.Lastname != nil {
		fields["lastName"] = *command.Lastname
	}
	if command.PhoneNumber != nil {
		fields["phoneNumber"] = *command.PhoneNumber
	}
	if command.BirthDay != nil {
		fields["birthDay"] = *command.BirthDay
	}
	if command.ImageURL != nil {
		fields["imageUrl"] = *command.ImageURL
	}
	if command.Language != nil {
		if !helpers.IsSupportedLanguage(*command.Language) {
			return nil, helpers.NewLocalizedError("localization.error.unsupported_language")
		}
		fields["language"] = helpers.NormalizeLanguage(*command.Language)
	}

	var updatedUser *entities.User
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		for _, change := range changes {
			if !change.restricted {
				continue
			}
			hasRecentUpdate, err := h.userHistoryRepository.ExistsByFilter(txCtx, bson.M{
				"userId": command.UserID, "columnName": change.columnName,
				"updateOn": bson.M{"$gte": now.Add(-profileFieldUpdateInterval)},
			})
			if err != nil {
				return err
			}
			if hasRecentUpdate {
				return helpers.NewRateLimitError("user.error.profile_field_update_limit", change.columnName)
			}
		}

		if err := h.userRepository.UpdateFields(txCtx, userObjectID, fields); err != nil {
			return err
		}
		if len(changes) > 0 {
			entries := make([]entities.UserInfoHistory, 0, len(changes))
			for _, change := range changes {
				entries = append(entries, entities.UserInfoHistory{
					UserId: command.UserID, ColumnName: change.columnName, Value: change.value, UpdateOn: now,
				})
			}
			if err := h.userHistoryRepository.InsertMany(txCtx, entries); err != nil {
				return err
			}
		}
		updatedUser, err = h.userRepository.FindByID(txCtx, userObjectID)
		return err
	})
	if err != nil {
		return nil, err
	}

	response := dtos.UserToResultModel(*updatedUser)
	return &response, nil
}

func profileChanges(command UpdateProfileCommand) []profileChange {
	changes := make([]profileChange, 0, 4)
	if command.Firstname != nil {
		changes = append(changes, profileChange{
			columnName: entities.UserInfoColumnFirstName, value: *command.Firstname, restricted: true,
		})
	}
	if command.Lastname != nil {
		changes = append(changes, profileChange{
			columnName: entities.UserInfoColumnLastName, value: *command.Lastname, restricted: true,
		})
	}
	if command.PhoneNumber != nil {
		changes = append(changes, profileChange{
			columnName: entities.UserInfoColumnPhoneNumber, value: *command.PhoneNumber,
		})
	}
	if command.BirthDay != nil {
		changes = append(changes, profileChange{
			columnName: entities.UserInfoColumnBirthDay, value: *command.BirthDay, restricted: true,
		})
	}
	return changes
}

var _ cqrs.CommandHandler[UpdateProfileCommand, *dtos.UserResultModel] = (*UpdateProfileHandler)(nil)
