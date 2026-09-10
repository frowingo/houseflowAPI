package services

import (
	"context"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"time"
)

type userInfoChange struct {
	columnName string
	value      any
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

func insertUserInfoHistory(
	ctx context.Context,
	repository *abstract.DbRepository[entities.UserInfoHistory],
	entries []entities.UserInfoHistory,
) error {
	if repository == nil || len(entries) == 0 {
		return nil
	}
	return repository.InsertMany(ctx, entries)
}
