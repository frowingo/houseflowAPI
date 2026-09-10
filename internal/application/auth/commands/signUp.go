package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/mongo"
)

type SignUpCommand struct {
	cqrs.Request[string]
	Email     string
	Password  string
	Firstname string
	Lastname  string
}

type SignUpHandler struct {
	userRepository        databaseAbstract.DbRepository[entities.User]
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory]
}

func NewSignUpHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	userHistoryRepository databaseAbstract.DbRepository[entities.UserInfoHistory],
) *SignUpHandler {
	return &SignUpHandler{
		userRepository:        userRepository,
		userHistoryRepository: userHistoryRepository,
	}
}

func (h *SignUpHandler) Handle(ctx context.Context, command SignUpCommand) (string, error) {
	user, err := h.userRepository.FindByColumn(ctx, "email", command.Email)
	if user != nil {
		return "", helpers.NewConflictError("auth.error.user_already_exists")
	}
	if err != nil && !helpers.IsApplicationError(err, "database.error.document_not_found") {
		return "", err
	}

	hashedPassword, err := helpers.HashPassword(command.Password)
	if err != nil {
		return "", err
	}

	now := time.Now()
	newUser := entities.User{
		Email: command.Email, HashPassword: hashedPassword,
		Firstname: command.Firstname, Lastname: command.Lastname,
		Language: "en", CreatedOn: now, UpdatedOn: now, LastLogin: now,
		IsActive: true, HouseIds: []string{}, Role: entities.Normal,
	}

	var createdUser *entities.User
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		createdUser, err = h.userRepository.Insert(txCtx, newUser)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return helpers.NewConflictError("auth.error.user_already_exists")
			}
			return err
		}

		historyEntries := []entities.UserInfoHistory{
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnFirstName, Value: createdUser.Firstname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnLastName, Value: createdUser.Lastname, UpdateOn: now},
			{UserId: createdUser.Id.Hex(), ColumnName: entities.UserInfoColumnRole, Value: int(createdUser.Role), UpdateOn: now},
		}
		return h.userHistoryRepository.InsertMany(txCtx, historyEntries)
	})
	if err != nil {
		return "", err
	}

	return helpers.GenerateToken(createdUser.Email, createdUser.Id.Hex(), int(createdUser.Role), createdUser.Language)
}

var _ cqrs.CommandHandler[SignUpCommand, string] = (*SignUpHandler)(nil)
