package commands

import (
	"context"
	"strconv"
	"strings"
	"time"

	houseApplication "houseflowApi/internal/application/house"
	housePolicies "houseflowApi/internal/application/house/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const houseProfileFieldUpdateInterval = 48 * time.Hour

type UpdateHouseProfileCommand struct {
	cqrs.Request[*dtos.HouseInfosResponseModel]
	HouseID               string
	RequesterID           string
	HouseName             *string
	HouseProfileImage     *string
	HouseMemberCountLimit *int
	HouseType             *entities.HouseType
}

type houseProfileChange struct {
	columnName string
	fieldName  string
	value      any
}

type UpdateHouseProfileHandler struct {
	membershipPolicy       *housePolicies.MembershipPolicy
	houseRepository        databaseAbstract.DbRepository[entities.House]
	houseHistoryRepository databaseAbstract.DbRepository[entities.HouseInfoHistory]
	infoReader             *houseApplication.InfoReader
}

func NewUpdateHouseProfileHandler(
	membershipPolicy *housePolicies.MembershipPolicy,
	houseRepository databaseAbstract.DbRepository[entities.House],
	houseHistoryRepository databaseAbstract.DbRepository[entities.HouseInfoHistory],
	infoReader *houseApplication.InfoReader,
) *UpdateHouseProfileHandler {
	return &UpdateHouseProfileHandler{
		membershipPolicy:       membershipPolicy,
		houseRepository:        houseRepository,
		houseHistoryRepository: houseHistoryRepository,
		infoReader:             infoReader,
	}
}

func (h *UpdateHouseProfileHandler) Handle(
	ctx context.Context,
	command UpdateHouseProfileCommand,
) (*dtos.HouseInfosResponseModel, error) {
	var response *dtos.HouseInfosResponseModel
	err := h.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		house, err := h.membershipPolicy.RequireOwner(txCtx, command.HouseID, command.RequesterID)
		if err != nil {
			return err
		}

		changes, fields, err := houseProfileChanges(command, house)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			response, err = h.infoReader.Read(txCtx, house)
			return err
		}

		now := time.Now().UTC()
		canonicalHouseID := house.Id.Hex()
		for _, change := range changes {
			hasRecentUpdate, err := h.houseHistoryRepository.ExistsByFilter(txCtx, bson.M{
				"houseId":    canonicalHouseID,
				"columnName": change.columnName,
				"updateOn":   bson.M{"$gte": now.Add(-houseProfileFieldUpdateInterval)},
			})
			if err != nil {
				return err
			}
			if hasRecentUpdate {
				return helpers.NewRateLimitError("house.error.profile_field_update_limit", change.fieldName)
			}
		}

		fields["updatedOn"] = now
		if err := h.houseRepository.UpdateFields(txCtx, house.Id, fields); err != nil {
			return err
		}

		histories := make([]entities.HouseInfoHistory, 0, len(changes))
		for _, change := range changes {
			histories = append(histories, entities.HouseInfoHistory{
				HouseId:    canonicalHouseID,
				ColumnName: change.columnName,
				Value:      change.value,
				UpdatedBy:  command.RequesterID,
				UpdateOn:   now,
			})
		}
		if err := h.houseHistoryRepository.InsertMany(txCtx, histories); err != nil {
			return err
		}

		updatedHouse, err := h.houseRepository.FindByID(txCtx, house.Id)
		if err != nil {
			return err
		}
		response, err = h.infoReader.Read(txCtx, updatedHouse)
		return err
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func houseProfileChanges(command UpdateHouseProfileCommand, house *entities.House) ([]houseProfileChange, bson.M, error) {
	changes := make([]houseProfileChange, 0, 4)
	fields := bson.M{}

	if command.HouseName != nil {
		value := strings.TrimSpace(*command.HouseName)
		if value != house.Name {
			fields["name"] = value
			changes = append(changes, houseProfileChange{entities.HouseInfoColumnName, "houseName", value})
		}
	}
	if command.HouseProfileImage != nil {
		value := strings.TrimSpace(*command.HouseProfileImage)
		if value != house.ProfileImage {
			fields["profileImage"] = value
			changes = append(changes, houseProfileChange{entities.HouseInfoColumnProfileImage, "houseProfileImage", value})
		}
	}
	if command.HouseMemberCountLimit != nil {
		value := *command.HouseMemberCountLimit
		if value < len(house.MemberIds) {
			return nil, nil, helpers.NewConflictError(
				"house.error.member_limit_below_current",
				strconv.Itoa(len(house.MemberIds)),
			)
		}
		if value != house.MaxMemberCount {
			fields["maxMemberCount"] = value
			changes = append(changes, houseProfileChange{entities.HouseInfoColumnMemberCountLimit, "houseMemberCountLimit", value})
		}
	}
	if command.HouseType != nil && *command.HouseType != house.Type {
		fields["type"] = *command.HouseType
		changes = append(changes, houseProfileChange{entities.HouseInfoColumnType, "houseType", *command.HouseType})
	}

	return changes, fields, nil
}

var _ cqrs.CommandHandler[UpdateHouseProfileCommand, *dtos.HouseInfosResponseModel] = (*UpdateHouseProfileHandler)(nil)
