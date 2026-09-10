package policies

import (
	"context"

	"houseflowApi/internal/abstract"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

// AssignmentPolicy owns the rules for assigning a chore to a house member.
type AssignmentPolicy struct {
	userRepository *abstract.DbRepository[entities.User]
}

func NewAssignmentPolicy(userRepository *abstract.DbRepository[entities.User]) *AssignmentPolicy {
	return &AssignmentPolicy{userRepository: userRepository}
}

func (p *AssignmentPolicy) RequireValidAssignee(ctx context.Context, house *entities.House, assigneeID string) error {
	assigneeObjectID, err := helpers.ToMongoId(assigneeID)
	if err != nil {
		return helpers.NewLocalizedError("chore.error.invalid_assignee_id")
	}
	if !housePolicies.ContainsMember(house.MemberIds, assigneeID) {
		return helpers.NewLocalizedError("chore.error.assignee_not_member")
	}
	if _, err := p.userRepository.FindByID(ctx, assigneeObjectID); err != nil {
		return helpers.NewLocalizedError("chore.error.assignee_not_found")
	}
	return nil
}
