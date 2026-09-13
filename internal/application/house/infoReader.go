package house

import (
	"context"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type InfoReader struct {
	userRepository databaseAbstract.DbRepository[entities.User]
}

func NewInfoReader(userRepository databaseAbstract.DbRepository[entities.User]) *InfoReader {
	return &InfoReader{userRepository: userRepository}
}

func (r *InfoReader) Read(ctx context.Context, house *entities.House) (*dtos.HouseInfosResponseModel, error) {
	memberObjectIDs := make([]primitive.ObjectID, 0, len(house.MemberIds))
	for _, memberID := range house.MemberIds {
		objectID, err := primitive.ObjectIDFromHex(memberID)
		if err == nil {
			memberObjectIDs = append(memberObjectIDs, objectID)
		}
	}

	membersByID := make(map[string]entities.User, len(memberObjectIDs))
	if len(memberObjectIDs) > 0 {
		members, err := r.userRepository.FindManyByFilter(ctx,
			bson.M{"_id": bson.M{"$in": memberObjectIDs}},
			options.Find().SetProjection(bson.M{"firstName": 1, "lastName": 1, "imageUrl": 1}),
		)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			membersByID[member.Id.Hex()] = member
		}
	}

	response := &dtos.HouseInfosResponseModel{
		HouseName:             house.Name,
		HouseProfileImage:     house.ProfileImage,
		HouseMemberCount:      len(house.MemberIds),
		HouseMemberCountLimit: house.MaxMemberCount,
		HouseType:             house.Type,
		HouseMembers:          make([]dtos.HouseMemberInfoModel, 0, len(house.MemberIds)),
	}
	for _, memberID := range house.MemberIds {
		member, exists := membersByID[memberID]
		if !exists {
			continue
		}
		response.HouseMembers = append(response.HouseMembers, dtos.HouseMemberInfoModel{
			UserId:       memberID,
			Name:         dtos.UserDisplayName(member),
			ProfileImage: member.ImageURL,
			IsOwner:      house.OwnerId == memberID,
		})
	}
	return response, nil
}
