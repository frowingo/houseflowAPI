package policies

import (
	"context"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

// MembershipPolicy owns the shared house membership authorization rule.
type MembershipPolicy struct {
	houseRepository databaseAbstract.DbRepository[entities.House]
}

func NewMembershipPolicy(houseRepository databaseAbstract.DbRepository[entities.House]) *MembershipPolicy {
	return &MembershipPolicy{houseRepository: houseRepository}
}

func (p *MembershipPolicy) RequireMember(ctx context.Context, houseID string, userID string) (*entities.House, error) {
	houseObjectID, err := helpers.ToMongoId(houseID)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}

	house, err := p.houseRepository.FindByID(ctx, houseObjectID)
	if err != nil {
		if helpers.IsApplicationError(err, "database.error.document_not_found") {
			return nil, helpers.NewNotFoundError("house.error.not_found")
		}
		return nil, err
	}
	if !ContainsMember(house.MemberIds, userID) {
		return nil, helpers.NewForbiddenError("house.error.user_not_member")
	}

	return house, nil
}

func (p *MembershipPolicy) RequireOwner(ctx context.Context, houseID string, userID string) (*entities.House, error) {
	house, err := p.RequireMember(ctx, houseID, userID)
	if err != nil {
		return nil, err
	}
	if house.OwnerId != userID {
		return nil, helpers.NewForbiddenError("house.error.owner_required")
	}
	return house, nil
}

func ContainsMember(memberIDs []string, userID string) bool {
	for _, memberID := range memberIDs {
		if memberID == userID {
			return true
		}
	}
	return false
}
