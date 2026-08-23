package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

const profileFieldUpdateInterval = 20 * 24 * time.Hour

type userInfoChange struct {
	columnName string
	value      any
	restricted bool
}

func newUserInfoHistoryEntries(user entities.User, updateOn time.Time) []entities.UserInfoHistory {
	changes := []userInfoChange{
		{columnName: entities.UserInfoColumnFirstName, value: user.Firstname},
		{columnName: entities.UserInfoColumnLastName, value: user.Lastname},
		{columnName: entities.UserInfoColumnRole, value: int(user.Role)},
	}
	if user.PhoneNumber != "" {
		changes = append(changes, userInfoChange{
			columnName: entities.UserInfoColumnPhoneNumber,
			value:      user.PhoneNumber,
		})
	}
	if !user.BirthDay.IsZero() {
		changes = append(changes, userInfoChange{
			columnName: entities.UserInfoColumnBirthDay,
			value:      user.BirthDay,
		})
	}

	return userInfoHistoryEntries(user.Id.Hex(), changes, updateOn)
}

func profileUserInfoChanges(model dtos.UpdateUserModel) []userInfoChange {

	changes := make([]userInfoChange, 0, 4)
	if model.Firstname != nil {
		changes = append(changes, userInfoChange{
			columnName: entities.UserInfoColumnFirstName,
			value:      *model.Firstname,
			restricted: true,
		})
	}
	if model.Lastname != nil {
		changes = append(changes, userInfoChange{
			columnName: entities.UserInfoColumnLastName,
			value:      *model.Lastname,
			restricted: true,
		})
	}
	if model.PhoneNumber != nil {
		changes = append(changes, userInfoChange{
			columnName: entities.UserInfoColumnPhoneNumber,
			value:      *model.PhoneNumber,
		})
	}
	if model.BirthDay != nil {
		changes = append(changes, userInfoChange{
			columnName: entities.UserInfoColumnBirthDay,
			value:      model.BirthDay.Time,
			restricted: true,
		})
	}

	return changes
}

func userInfoHistoryEntries(userId string, changes []userInfoChange, updateOn time.Time) []entities.UserInfoHistory {
	entries := make([]entities.UserInfoHistory, 0, len(changes))
	for _, change := range changes {
		entries = append(entries, entities.UserInfoHistory{
			UserId:     userId,
			ColumnName: change.columnName,
			Value:      change.value,
			UpdateOn:   updateOn,
		})
	}
	return entries
}

func validateProfileUpdateIntervals(
	repository *abstract.DbRepository[entities.UserInfoHistory],
	userId string,
	changes []userInfoChange,
	now time.Time,
) error {

	if repository == nil {
		return nil
	}

	for _, change := range changes {

		if !change.restricted {
			continue
		}

		hasRecentUpdate, err := repository.ExistsByFilter(
			recentUserInfoHistoryFilter(userId, change.columnName, now),
		)

		if err != nil {
			return err
		}

		if hasRecentUpdate {
			return helpers.NewLocalizedError("user.error.profile_field_update_limit", change.columnName)
		}
	}

	return nil
}

func recentUserInfoHistoryFilter(userId string, columnName string, now time.Time) bson.M {
	return bson.M{
		"userId":     userId,
		"columnName": columnName,
		"updateOn": bson.M{
			"$gte": now.Add(-profileFieldUpdateInterval),
		},
	}
}

func insertUserInfoHistory(
	repository *abstract.DbRepository[entities.UserInfoHistory],
	entries []entities.UserInfoHistory,
) error {
	if repository == nil || len(entries) == 0 {
		return nil
	}
	return repository.InsertMany(entries)
}
