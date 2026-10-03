package database

import (
	"context"
	"errors"
	"strings"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type GameParticipantDirectory struct{ db *mongo.Database }

func NewGameParticipantDirectory(client *mongo.Client, dbName string) *GameParticipantDirectory {
	return &GameParticipantDirectory{db: client.Database(dbName)}
}

func (directory *GameParticipantDirectory) ReadHouseParticipants(ctx context.Context, houseID string) ([]gameAbstract.GameParticipant, error) {
	id, err := helpers.ToMongoId(houseID)
	if err != nil {
		return nil, err
	}
	var house struct {
		Members []string `bson:"memberIds"`
	}
	err = directory.db.Collection("House").FindOne(ctx, bson.M{"_id": id}, options.FindOne().SetProjection(bson.M{"memberIds": 1})).Decode(&house)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return []gameAbstract.GameParticipant{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]primitive.ObjectID, 0, len(house.Members))
	for _, member := range house.Members {
		if objectID, err := primitive.ObjectIDFromHex(member); err == nil {
			ids = append(ids, objectID)
		}
	}
	cursor, err := directory.db.Collection("User").Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "isActive": true}, options.Find().SetProjection(bson.M{"firstName": 1, "lastName": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	participants := make([]gameAbstract.GameParticipant, 0, len(ids))
	for cursor.Next(ctx) {
		var user entities.User
		if err := cursor.Decode(&user); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(user.Firstname + " " + user.Lastname)
		if name == "" {
			name = "Player"
		}
		characters := []rune(name)
		if len(characters) > 128 {
			name = string(characters[:128])
		}
		participants = append(participants, gameAbstract.GameParticipant{PlayerID: user.Id.Hex(), DisplayName: name})
	}
	return participants, cursor.Err()
}

var _ gameAbstract.GameParticipantDirectory = (*GameParticipantDirectory)(nil)
