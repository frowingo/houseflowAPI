package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/mongo"
)

type CreateUserCommand struct {
	cqrs.Request[*dtos.NewUserModel]
	Firstname   string
	Lastname    string
	PhoneNumber string
	Email       string
	Password    string
	BirthDay    time.Time
	Language    string
}

type CreateUserHandler struct {
	userRepository        databaseAbstract.DbRepository[entities.User]
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory]
}

func NewCreateUserHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory],
) *CreateUserHandler {
	return &CreateUserHandler{
		userRepository:        userRepository,
		userHistoryRepository: userHistoryRepository,
	}
}

func (h *CreateUserHandler) Handle(ctx context.Context, command CreateUserCommand) (*dtos.NewUserModel, error) {
	hashedPassword, err := helpers.HashPassword(command.Password)
	if err != nil {
		return nil, err
	}

	language := helpers.NormalizeLanguage(command.Language)
	if language == "" {
		language = "en"
	}
	now := time.Now()
	entity := entities.User{
		Firstname: command.Firstname, Lastname: command.Lastname, PhoneNumber: command.PhoneNumber,
		Email: command.Email, HashPassword: hashedPassword, BirthDay: command.BirthDay,
		Language: language, HouseIds: []string{}, IsActive: true,
		CreatedOn: now, UpdatedOn: now, LastLogin: now, Role: entities.Normal,
	}

	var createdUser *entities.User
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		createdUser, err = h.userRepository.Insert(txCtx, entity)
		if err != nil {
			return err
		}

		entries := []entities.UserInfoHistory{
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnFirstName, Value: createdUser.Firstname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnLastName, Value: createdUser.Lastname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnRole, Value: int(createdUser.Role), UpdateOn: now},
		}
		if createdUser.PhoneNumber != "" {
			entries = append(entries, entities.UserInfoHistory{
				UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnPhoneNumber,
				Value: createdUser.PhoneNumber, UpdateOn: now,
			})
		}
		if !createdUser.BirthDay.IsZero() {
			entries = append(entries, entities.UserInfoHistory{
				UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnBirthDay,
				Value: createdUser.BirthDay, UpdateOn: now,
			})
		}
		return h.userHistoryRepository.InsertMany(txCtx, entries)
	})
	if err != nil {
		return nil, err
	}

	return &dtos.NewUserModel{
		Firstname: command.Firstname, Lastname: command.Lastname, PhoneNumber: command.PhoneNumber,
		Email: command.Email, Password: command.Password, BirthDay: dtos.NewUTCDateTime(command.BirthDay),
		Language: command.Language,
	}, nil
}

var _ cqrs.CommandHandler[CreateUserCommand, *dtos.NewUserModel] = (*CreateUserHandler)(nil)
