package commands

import (
	"context"
	"time"

	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ChoreReminderWindow is how long before the due date the other members of
// a house may remind the assignee.
const ChoreReminderWindow = 12 * time.Hour

// RemindChoreCommand lets a house member remind the assignee of a chore
// that is due soon. Each member can remind once per chore; the unique index
// on the reminder notification enforces it.
type RemindChoreCommand struct {
	cqrs.Request[cqrs.NoResult]
	ChoreID  string
	SenderID string
}

type reminderEntityReader[T any] interface {
	FindByID(ctx context.Context, id primitive.ObjectID) (*T, error)
}

type reminderMembershipPolicy interface {
	RequireMember(ctx context.Context, houseID string, userID string) (*entities.House, error)
}

type RemindChoreHandler struct {
	choreRepository  reminderEntityReader[entities.Chore]
	userRepository   reminderEntityReader[entities.User]
	membershipPolicy reminderMembershipPolicy
	notifier         notificationAbstract.Notifier
	now              func() time.Time
}

func NewRemindChoreHandler(
	choreRepository reminderEntityReader[entities.Chore],
	userRepository reminderEntityReader[entities.User],
	membershipPolicy reminderMembershipPolicy,
	notifier notificationAbstract.Notifier,
	options ...RemindChoreOption,
) *RemindChoreHandler {
	handler := &RemindChoreHandler{
		choreRepository:  choreRepository,
		userRepository:   userRepository,
		membershipPolicy: membershipPolicy,
		notifier:         notifier,
		now:              time.Now,
	}
	for _, option := range options {
		option(handler)
	}
	return handler
}

type RemindChoreOption func(*RemindChoreHandler)

// WithReminderClock replaces the clock that decides the reminder window.
func WithReminderClock(now func() time.Time) RemindChoreOption {
	return func(handler *RemindChoreHandler) { handler.now = now }
}

func (h *RemindChoreHandler) Handle(ctx context.Context, command RemindChoreCommand) (cqrs.NoResult, error) {
	choreObjectID, err := helpers.ToMongoId(command.ChoreID)
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("chore.error.invalid_chore_id", command.ChoreID)
	}
	chore, err := h.choreRepository.FindByID(ctx, choreObjectID)
	if err != nil {
		if helpers.IsApplicationError(err, "database.error.document_not_found") {
			return cqrs.NoResult{}, helpers.NewNotFoundError("chore.error.not_found", command.ChoreID)
		}
		return cqrs.NoResult{}, err
	}
	if _, err := h.membershipPolicy.RequireMember(ctx, chore.HouseId, command.SenderID); err != nil {
		return cqrs.NoResult{}, err
	}
	if err := checkReminderAllowed(chore, command.SenderID, h.now()); err != nil {
		return cqrs.NoResult{}, err
	}

	senderObjectID, err := helpers.ToMongoId(command.SenderID)
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.invalid_user_id")
	}
	sender, err := h.userRepository.FindByID(ctx, senderObjectID)
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.not_found")
	}

	_, err = h.notifier.Notify(ctx, notificationAbstract.NotificationRequest{
		Type:             entities.ChoreReminderNotification,
		HouseID:          chore.HouseId,
		SenderUserID:     command.SenderID,
		RecipientUserIDs: []string{chore.AssignedTo},
		ChoreID:          chore.Id.Hex(),
		Title:            "notification.chore_reminder.title",
		Message:          "notification.chore_reminder.message",
		Localized:        true,
		Args:             []string{dtos.UserDisplayName(*sender), chore.Title},
	})
	if mongo.IsDuplicateKeyError(err) {
		return cqrs.NoResult{}, helpers.NewConflictError("chore.reminder.error.already_sent")
	}
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("notification.error.failed_create")
	}
	return cqrs.NoResult{}, nil
}

func checkReminderAllowed(chore *entities.Chore, senderID string, now time.Time) error {
	switch {
	case chore.AssignedTo == "":
		return helpers.NewLocalizedError("chore.reminder.error.no_assignee")
	case chore.AssignedTo == senderID:
		return helpers.NewLocalizedError("chore.reminder.error.self_reminder")
	case chore.Status != entities.Draft && chore.Status != entities.Progress:
		return helpers.NewLocalizedError("chore.reminder.error.invalid_status")
	case !now.Before(chore.DueDate) || now.Before(chore.DueDate.Add(-ChoreReminderWindow)):
		return helpers.NewLocalizedError("chore.reminder.error.outside_window")
	}
	return nil
}

var _ cqrs.CommandHandler[RemindChoreCommand, cqrs.NoResult] = (*RemindChoreHandler)(nil)
