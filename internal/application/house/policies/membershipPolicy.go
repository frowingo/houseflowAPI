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
		return nil, helpers.NewLocalizedError("house.error.not_found")
	}
	if !ContainsMember(house.MemberIds, userID) {
		return nil, helpers.NewLocalizedError("house.error.user_not_member")
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
