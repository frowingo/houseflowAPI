package queries

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ChoreArchivePageSize is the most chores one archive page returns.
const ChoreArchivePageSize = 10

// GetChoreArchiveQuery lists the closed (completed or failed) chores of a
// house whose completedAt is in [From, To), newest first. Cursor is the
// NextCursor of the previous page, empty for the first one.
type GetChoreArchiveQuery struct {
	cqrs.Request[*dtos.ChoreArchivePageModel]
	HouseID     string
	RequesterID string
	From        time.Time
	To          time.Time
	Cursor      string
}

type choreArchiveMembershipPolicy interface {
	RequireMember(ctx context.Context, houseID string, userID string) (*entities.House, error)
}

type GetChoreArchiveHandler struct {
	chores           databaseAbstract.DbRepository[entities.Chore]
	histories        databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	votes            databaseAbstract.DbRepository[entities.ChoreReviewVote]
	membershipPolicy choreArchiveMembershipPolicy
}

func NewGetChoreArchiveHandler(
	chores databaseAbstract.DbRepository[entities.Chore],
	histories databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	votes databaseAbstract.DbRepository[entities.ChoreReviewVote],
	membershipPolicy choreArchiveMembershipPolicy,
) *GetChoreArchiveHandler {
	return &GetChoreArchiveHandler{chores: chores, histories: histories, votes: votes, membershipPolicy: membershipPolicy}
}

func (h *GetChoreArchiveHandler) Handle(ctx context.Context, query GetChoreArchiveQuery) (*dtos.ChoreArchivePageModel, error) {
	if _, err := helpers.ToMongoId(query.HouseID); err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) {
		return nil, helpers.NewLocalizedError("chore.error.archive_invalid_range")
	}
	if _, err := h.membershipPolicy.RequireMember(ctx, query.HouseID, query.RequesterID); err != nil {
		return nil, err
	}

	filter := bson.M{
		"houseId":     query.HouseID,
		"status":      bson.M{"$in": bson.A{entities.Completed, entities.Failed}},
		"completedAt": bson.M{"$gte": query.From, "$lt": query.To},
	}
	if query.Cursor != "" {
		completedAt, id, ok := decodeArchiveCursor(query.Cursor)
		if !ok {
			return nil, helpers.NewLocalizedError("chore.error.archive_invalid_cursor")
		}
		filter["$or"] = bson.A{
			bson.M{"completedAt": bson.M{"$lt": completedAt}},
			bson.M{"completedAt": completedAt, "_id": bson.M{"$lt": id}},
		}
	}

	// One extra chore tells whether another page exists.
	chores, err := h.chores.FindManyByFilter(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "completedAt", Value: -1}, {Key: "_id", Value: -1}}).
		SetLimit(ChoreArchivePageSize+1))
	if err != nil {
		return nil, err
	}

	page := &dtos.ChoreArchivePageModel{}
	if len(chores) > ChoreArchivePageSize {
		chores = chores[:ChoreArchivePageSize]
		last := chores[len(chores)-1]
		page.NextCursor = encodeArchiveCursor(last.CompletedAt, last.Id)
	}

	choreIDs := make([]string, 0, len(chores))
	for _, chore := range chores {
		choreIDs = append(choreIDs, chore.Id.Hex())
	}
	historiesByChore := make(map[string][]entities.ChoreStatusHistory)
	votesByChore := make(map[string][]entities.ChoreReviewVote)
	if len(choreIDs) > 0 {
		byChore := bson.M{"choreId": bson.M{"$in": choreIDs}}
		histories, err := h.histories.FindManyByFilter(ctx, byChore,
			options.Find().SetSort(bson.D{{Key: "dateTime", Value: 1}, {Key: "_id", Value: 1}}))
		if err != nil {
			return nil, err
		}
		for _, history := range histories {
			historiesByChore[history.ChoreId] = append(historiesByChore[history.ChoreId], history)
		}
		votes, err := h.votes.FindManyByFilter(ctx, byChore,
			options.Find().SetSort(bson.D{{Key: "createdOn", Value: 1}, {Key: "_id", Value: 1}}))
		if err != nil {
			return nil, err
		}
		for _, vote := range votes {
			votesByChore[vote.ChoreId] = append(votesByChore[vote.ChoreId], vote)
		}
	}

	page.Chores = make([]dtos.ChoreResponseModel, 0, len(chores))
	for _, chore := range chores {
		id := chore.Id.Hex()
		page.Chores = append(page.Chores,
			dtos.ChoreToResponseModelWithReview(chore, historiesByChore[id], votesByChore[id]))
	}
	return page, nil
}

// The cursor is the sort key of the last chore on the page: completedAt in
// Unix milliseconds, which is the precision MongoDB stores, and the ObjectID.
func encodeArchiveCursor(completedAt time.Time, id primitive.ObjectID) string {
	raw := strconv.FormatInt(completedAt.UnixMilli(), 10) + ":" + id.Hex()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeArchiveCursor(cursor string) (time.Time, primitive.ObjectID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, primitive.NilObjectID, false
	}
	millis, hex, found := strings.Cut(string(raw), ":")
	if !found {
		return time.Time{}, primitive.NilObjectID, false
	}
	unixMilli, err := strconv.ParseInt(millis, 10, 64)
	if err != nil {
		return time.Time{}, primitive.NilObjectID, false
	}
	id, err := primitive.ObjectIDFromHex(hex)
	if err != nil {
		return time.Time{}, primitive.NilObjectID, false
	}
	return time.UnixMilli(unixMilli).UTC(), id, true
}

var _ cqrs.QueryHandler[GetChoreArchiveQuery, *dtos.ChoreArchivePageModel] = (*GetChoreArchiveHandler)(nil)
