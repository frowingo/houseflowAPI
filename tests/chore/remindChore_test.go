package chore

import (
	"context"
	"errors"
	"testing"
	"time"

	choreCommands "houseflowApi/internal/application/chore/commands"
	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type fakeReminderReader[T any] struct{ entity *T }

func (f fakeReminderReader[T]) FindByID(context.Context, primitive.ObjectID) (*T, error) {
	if f.entity == nil {
		return nil, helpers.NewNotFoundError("database.error.document_not_found")
	}
	return f.entity, nil
}

type fakeReminderMembership struct{ members []string }

func (f fakeReminderMembership) RequireMember(_ context.Context, _ string, userID string) (*entities.House, error) {
	for _, member := range f.members {
		if member == userID {
			return &entities.House{}, nil
		}
	}
	return nil, helpers.NewForbiddenError("house.error.not_member")
}

type fakeNotifier struct {
	requests []notificationAbstract.NotificationRequest
	err      error
}

func (f *fakeNotifier) Notify(_ context.Context, request notificationAbstract.NotificationRequest) (*entities.Notification, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return nil, f.err
	}
	return &entities.Notification{}, nil
}

const (
	reminderSenderID   = "650000000000000000000001"
	reminderAssigneeID = "650000000000000000000002"
)

func newReminderHandler(chore *entities.Chore, notifier *fakeNotifier, now time.Time) *choreCommands.RemindChoreHandler {
	return choreCommands.NewRemindChoreHandler(
		fakeReminderReader[entities.Chore]{entity: chore},
		fakeReminderReader[entities.User]{entity: &entities.User{Firstname: "Ayşe", Lastname: "Yılmaz"}},
		fakeReminderMembership{members: []string{reminderSenderID, reminderAssigneeID}},
		notifier,
		choreCommands.WithReminderClock(func() time.Time { return now }),
	)
}

func reminderChore(dueDate time.Time, status entities.ChoreStatus) *entities.Chore {
	return &entities.Chore{
		Id: primitive.NewObjectID(), Title: "Bulaşık", HouseId: "house-1",
		AssignedTo: reminderAssigneeID, DueDate: dueDate, Status: status,
	}
}

func TestRemindChoreNotifiesAssigneeInsideWindow(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, status := range []entities.ChoreStatus{entities.Draft, entities.Progress} {
		chore := reminderChore(now.Add(11*time.Hour), status)
		notifier := &fakeNotifier{}
		_, err := newReminderHandler(chore, notifier, now).Handle(context.Background(), choreCommands.RemindChoreCommand{
			ChoreID: chore.Id.Hex(), SenderID: reminderSenderID,
		})
		if err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		if len(notifier.requests) != 1 {
			t.Fatalf("status %d: notify count = %d", status, len(notifier.requests))
		}
		request := notifier.requests[0]
		if request.Type != entities.ChoreReminderNotification || request.ChoreID != chore.Id.Hex() ||
			request.SenderUserID != reminderSenderID || len(request.RecipientUserIDs) != 1 ||
			request.RecipientUserIDs[0] != reminderAssigneeID || !request.Localized {
			t.Fatalf("unexpected request: %+v", request)
		}
		if len(request.Args) != 2 || request.Args[0] != "Ayşe Yılmaz" || request.Args[1] != "Bulaşık" {
			t.Fatalf("unexpected args: %v", request.Args)
		}
	}
}

func TestRemindChoreRejectsWhenNotAllowed(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		chore    *entities.Chore
		senderID string
		key      string
	}{
		{"before window", reminderChore(now.Add(13*time.Hour), entities.Progress), reminderSenderID, "chore.reminder.error.outside_window"},
		{"past due", reminderChore(now, entities.Progress), reminderSenderID, "chore.reminder.error.outside_window"},
		{"in review", reminderChore(now.Add(time.Hour), entities.InTest), reminderSenderID, "chore.reminder.error.invalid_status"},
		{"completed", reminderChore(now.Add(time.Hour), entities.Completed), reminderSenderID, "chore.reminder.error.invalid_status"},
		{"assignee", reminderChore(now.Add(time.Hour), entities.Progress), reminderAssigneeID, "chore.reminder.error.self_reminder"},
		{"no assignee", func() *entities.Chore {
			chore := reminderChore(now.Add(time.Hour), entities.Progress)
			chore.AssignedTo = ""
			return chore
		}(), reminderSenderID, "chore.reminder.error.no_assignee"},
		{"not member", reminderChore(now.Add(time.Hour), entities.Progress), "650000000000000000000009", "house.error.not_member"},
	}
	for _, testCase := range cases {
		notifier := &fakeNotifier{}
		_, err := newReminderHandler(testCase.chore, notifier, now).Handle(context.Background(), choreCommands.RemindChoreCommand{
			ChoreID: testCase.chore.Id.Hex(), SenderID: testCase.senderID,
		})
		if !helpers.IsApplicationError(err, testCase.key) {
			t.Fatalf("%s: err = %v, want %s", testCase.name, err, testCase.key)
		}
		if len(notifier.requests) != 0 {
			t.Fatalf("%s: notified", testCase.name)
		}
	}
}

func TestRemindChoreMapsDuplicateReminderToAlreadySent(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	chore := reminderChore(now.Add(time.Hour), entities.Progress)
	notifier := &fakeNotifier{err: mongo.WriteException{WriteErrors: []mongo.WriteError{{Code: 11000}}}}
	_, err := newReminderHandler(chore, notifier, now).Handle(context.Background(), choreCommands.RemindChoreCommand{
		ChoreID: chore.Id.Hex(), SenderID: reminderSenderID,
	})
	var applicationError *helpers.ApplicationError
	if !helpers.IsApplicationError(err, "chore.reminder.error.already_sent") {
		t.Fatalf("err = %v", err)
	}
	if !errors.As(err, &applicationError) || applicationError.Kind != helpers.ErrorKindConflict {
		t.Fatalf("kind = %d, want conflict", applicationError.Kind)
	}
}

func TestRemindChoreMapsMissingChoreToNotFound(t *testing.T) {
	notifier := &fakeNotifier{}
	_, err := newReminderHandler(nil, notifier, time.Now()).Handle(context.Background(), choreCommands.RemindChoreCommand{
		ChoreID: primitive.NewObjectID().Hex(), SenderID: reminderSenderID,
	})
	if !helpers.IsApplicationError(err, "chore.error.not_found") {
		t.Fatalf("err = %v", err)
	}
}
