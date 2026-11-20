package queries

import (
	"context"

	"houseflowApi/internal/application/notification"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultNotificationPageSize = 20
	maxNotificationPageSize     = 50
)

// GetNotificationsQuery lists the house notifications the requester can see,
// newest first, rendered in Language. Notifications the requester sent are
// left out. Before is the cursor returned by the previous page.
type GetNotificationsQuery struct {
	cqrs.Request[*dtos.NotificationPageModel]
	HouseID     string
	RequesterID string
	Language    string
	Before      string
	Limit       int
}

type notificationMembershipPolicy interface {
	RequireMember(ctx context.Context, houseID string, userID string) (*entities.House, error)
}

type GetNotificationsHandler struct {
	notifications    databaseAbstract.DbRepository[entities.Notification]
	membershipPolicy notificationMembershipPolicy
	localizer        helpers.MessageLocalizer
}

func NewGetNotificationsHandler(
	notifications databaseAbstract.DbRepository[entities.Notification],
	membershipPolicy notificationMembershipPolicy,
	localizer helpers.MessageLocalizer,
) *GetNotificationsHandler {
	return &GetNotificationsHandler{notifications: notifications, membershipPolicy: membershipPolicy, localizer: localizer}
}

func (h *GetNotificationsHandler) Handle(ctx context.Context, query GetNotificationsQuery) (*dtos.NotificationPageModel, error) {
	if _, err := h.membershipPolicy.RequireMember(ctx, query.HouseID, query.RequesterID); err != nil {
		return nil, err
	}
	filter := bson.M{
		"houseId":      query.HouseID,
		"senderUserId": bson.M{"$ne": query.RequesterID},
		"$or": bson.A{
			bson.M{"recipientUserIds": bson.M{"$exists": false}},
			bson.M{"recipientUserIds": query.RequesterID},
		},
	}
	if query.Before != "" {
		before, err := helpers.ToMongoId(query.Before)
		if err != nil {
			return nil, helpers.NewLocalizedError("notification.error.invalid_cursor")
		}
		filter["_id"] = bson.M{"$lt": before}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultNotificationPageSize
	}
	limit = min(limit, maxNotificationPageSize)

	// One extra document tells whether an older page exists.
	items, err := h.notifications.FindManyByFilter(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(int64(limit+1)))
	if err != nil {
		return nil, helpers.NewLocalizedError("notification.error.failed_list")
	}

	page := &dtos.NotificationPageModel{Items: make([]dtos.NotificationResponseModel, 0, min(len(items), limit))}
	if len(items) > limit {
		items = items[:limit]
		page.NextCursor = items[limit-1].Id.Hex()
	}
	for _, item := range items {
		title, message := notification.Render(ctx, h.localizer, query.Language, item)
		page.Items = append(page.Items, dtos.NotificationResponseModel{
			Id:           item.Id.Hex(),
			Type:         string(item.Type),
			HouseId:      item.HouseId,
			SenderUserId: item.SenderUserId,
			ChoreId:      item.ChoreId,
			Title:        title,
			Message:      message,
			CreatedAt:    dtos.NewUTCDateTime(item.CreatedAt),
		})
	}
	return page, nil
}

var _ cqrs.QueryHandler[GetNotificationsQuery, *dtos.NotificationPageModel] = (*GetNotificationsHandler)(nil)
