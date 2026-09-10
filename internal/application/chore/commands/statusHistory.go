package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
)

func addStatusHistory(
	ctx context.Context,
	repository databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	choreID string,
	status entities.ChoreStatus,
	updaterID string,
	now time.Time,
) error {
	_, err := repository.Insert(ctx, entities.ChoreStatusHistory{
		ChoreId:  choreID,
		Status:   status,
		DateTime: now,
		Updater:  updaterID,
	})
	return err
}
