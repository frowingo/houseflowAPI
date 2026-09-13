package queries

import (
	"context"

	houseApplication "houseflowApi/internal/application/house"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

type GetHouseInfosQuery struct {
	cqrs.Request[*dtos.HouseInfosResponseModel]
	HouseID     string
	RequesterID string
}

type GetHouseInfosHandler struct {
	membershipPolicy *housePolicies.MembershipPolicy
	infoReader       *houseApplication.InfoReader
}

func NewGetHouseInfosHandler(
	membershipPolicy *housePolicies.MembershipPolicy,
	infoReader *houseApplication.InfoReader,
) *GetHouseInfosHandler {
	return &GetHouseInfosHandler{
		membershipPolicy: membershipPolicy,
		infoReader:       infoReader,
	}
}

func (h *GetHouseInfosHandler) Handle(
	ctx context.Context,
	query GetHouseInfosQuery,
) (*dtos.HouseInfosResponseModel, error) {
	house, err := h.membershipPolicy.RequireMember(ctx, query.HouseID, query.RequesterID)
	if err != nil {
		return nil, err
	}
	return h.infoReader.Read(ctx, house)
}

var _ cqrs.QueryHandler[GetHouseInfosQuery, *dtos.HouseInfosResponseModel] = (*GetHouseInfosHandler)(nil)
