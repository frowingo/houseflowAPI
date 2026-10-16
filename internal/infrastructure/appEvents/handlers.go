package appEvents

import (
	"context"

	userCommands "houseflowApi/internal/application/user/commands"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
)

// NotifyAfter runs a best-effort notification only after a command succeeds.
func NotifyAfter[Command, Result any](
	handler cqrs.Handler[Command, Result],
	notify func(context.Context, Command, Result),
) cqrs.Handler[Command, Result] {
	return afterSuccess[Command, Result]{handler: handler, notify: notify}
}

type afterSuccess[Command, Result any] struct {
	handler cqrs.Handler[Command, Result]
	notify  func(context.Context, Command, Result)
}

func (h afterSuccess[Command, Result]) Handle(ctx context.Context, command Command) (Result, error) {
	result, err := h.handler.Handle(ctx, command)
	if err == nil {
		h.notify(ctx, command, result)
	}
	return result, err
}

// User deletion removes house membership, so the affected houses are read first.
func (service *Service) NotifyDeleteUser(
	handler cqrs.Handler[userCommands.DeleteUserCommand, cqrs.NoResult],
	houses databaseAbstract.DbRepository[entities.House],
) cqrs.Handler[userCommands.DeleteUserCommand, cqrs.NoResult] {
	return deleteUserNotificationHandler{handler: handler, houses: houses, events: service}
}

type deleteUserNotificationHandler struct {
	handler cqrs.Handler[userCommands.DeleteUserCommand, cqrs.NoResult]
	houses  databaseAbstract.DbRepository[entities.House]
	events  *Service
}

func (h deleteUserNotificationHandler) Handle(ctx context.Context, command userCommands.DeleteUserCommand) (cqrs.NoResult, error) {
	var houseIDs []string
	if _, err := helpers.ToMongoId(command.UserID); err == nil {
		if houses, err := h.houses.FindManyByFilter(ctx, bson.M{"memberIds": command.UserID}); err == nil {
			for _, house := range houses {
				houseIDs = append(houseIDs, house.Id.Hex())
			}
		}
	}
	result, err := h.handler.Handle(ctx, command)
	if err == nil {
		h.events.UsersChanged(ctx, command.UserID)
		for _, houseID := range houseIDs {
			h.events.HouseChanged(ctx, houseID, false)
		}
	}
	return result, err
}
