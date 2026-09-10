package queries

import (
	"context"

	"houseflowApi/internal/abstract"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type GetUsersByHouseQuery struct {
	cqrs.Request[[]dtos.UserResultModel]
	HouseID     string
	RequesterID string
}

type GetUsersByHouseHandler struct {
	userRepository   *abstract.DbRepository[entities.User]
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewGetUsersByHouseHandler(
	userRepository *abstract.DbRepository[entities.User],
	membershipPolicy *housePolicies.MembershipPolicy,
) *GetUsersByHouseHandler {
	return &GetUsersByHouseHandler{
		userRepository:   userRepository,
		membershipPolicy: membershipPolicy,
	}
}

func (h *GetUsersByHouseHandler) Handle(ctx context.Context, query GetUsersByHouseQuery) ([]dtos.UserResultModel, error) {
	house, err := h.membershipPolicy.RequireMember(ctx, query.HouseID, query.RequesterID)
	if err != nil {
		return nil, err
	}

	memberObjectIDs := make([]primitive.ObjectID, 0, len(house.MemberIds))
	for _, memberID := range house.MemberIds {
		objectID, err := primitive.ObjectIDFromHex(memberID)
		if err == nil {
			memberObjectIDs = append(memberObjectIDs, objectID)
		}
	}
	if len(memberObjectIDs) == 0 {
		return []dtos.UserResultModel{}, nil
	}

	members, err := h.userRepository.FindManyByFilter(ctx, bson.M{"_id": bson.M{"$in": memberObjectIDs}})
	if err != nil {
		return nil, err
	}
	membersByID := make(map[string]entities.User, len(members))
	for _, member := range members {
		membersByID[member.Id.Hex()] = member
	}

	responses := make([]dtos.UserResultModel, 0, len(members))
	for _, memberID := range house.MemberIds {
		if member, exists := membersByID[memberID]; exists {
			responses = append(responses, dtos.UserToResultModel(member))
		}
	}
	return responses, nil
}

var _ cqrs.QueryHandler[GetUsersByHouseQuery, []dtos.UserResultModel] = (*GetUsersByHouseHandler)(nil)
