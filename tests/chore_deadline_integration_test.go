package tests

import (
	"testing"
	"time"

	choreApplication "houseflowApi/internal/application/chore"
	choreCommands "houseflowApi/internal/application/chore/commands"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestOverdueChoreCannotAdvanceBeforeDeadlineWorkerRuns(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 1)
	created, err := cqrs.Send[*dtos.ChoreResponseModel](fixture.ctx, fixture.sender, createChoreCommand(dtos.CreateChoreModel{
		Title: "Overdue chore", Description: "Deadline command guard", AssignedTo: owner.Id.Hex(),
		DueDate: dtos.NewUTCDateTime(time.Now().Add(-time.Minute)), HouseId: house.Id.Hex(), Level: entities.Easy,
	}, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}

	result, err := cqrs.Send[[]dtos.ChoreResponseModel](fixture.ctx, fixture.sender, choreCommands.UpdateChoreStatusCommand{
		HouseID: house.Id.Hex(), UserID: owner.Id.Hex(),
		Chores: []choreCommands.ChoreStatusUpdate{{ChoreID: created.Id, Status: entities.Progress}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Status != entities.Failed || result[0].IsCompleted {
		t.Fatalf("overdue advance result = %+v, want failed", result)
	}
	if len(result[0].StatusHistories) != 2 || result[0].StatusHistories[1].Status != entities.Failed {
		t.Fatalf("overdue histories = %+v, want draft then failed", result[0].StatusHistories)
	}
}

func TestChoreDeadlineReconcileFailsOnlyOverdueChores(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	dueDate := now.Add(-time.Minute)
	houseID := "deadline-house"
	chores := database.NewDbContext[entities.Chore](fixture.db.Client(), fixture.db.Name())
	histories := database.NewDbContext[entities.ChoreStatusHistory](fixture.db.Client(), fixture.db.Name())

	overdue, err := chores.Insert(fixture.ctx, entities.Chore{
		Title: "Overdue", HouseId: houseID, DueDate: dueDate, Status: entities.Progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	earlyCompleted, err := chores.Insert(fixture.ctx, entities.Chore{
		Title: "Early", HouseId: houseID, DueDate: dueDate, Status: entities.Completed,
		IsCompleted: true, CompletedAt: dueDate.Add(-time.Second), CompletedBy: "assignee",
	})
	if err != nil {
		t.Fatal(err)
	}
	lateCompleted, err := chores.Insert(fixture.ctx, entities.Chore{
		Title: "Late", HouseId: houseID, DueDate: dueDate, Status: entities.Completed,
		IsCompleted: true, CompletedAt: dueDate, CompletedBy: "assignee",
	})
	if err != nil {
		t.Fatal(err)
	}
	future, err := chores.Insert(fixture.ctx, entities.Chore{
		Title: "Future", HouseId: houseID, DueDate: now.Add(time.Minute), Status: entities.Progress,
	})
	if err != nil {
		t.Fatal(err)
	}

	service := choreApplication.NewDeadlineService(chores, histories, chorePolicies.NewWorkflowPolicy(), nil)
	houseIDs, err := service.Reconcile(fixture.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(houseIDs) != 1 || houseIDs[0] != houseID {
		t.Fatalf("affected houses = %v, want [%s]", houseIDs, houseID)
	}

	assertChoreStatus(t, fixture, overdue.Id, entities.Failed, false)
	assertChoreStatus(t, fixture, lateCompleted.Id, entities.Failed, false)
	assertChoreStatus(t, fixture, earlyCompleted.Id, entities.Completed, true)
	assertChoreStatus(t, fixture, future.Id, entities.Progress, false)

	failedHistories, err := fixture.db.Collection("ChoreStatusHistory").CountDocuments(
		fixture.ctx, bson.M{"status": entities.Failed},
	)
	if err != nil {
		t.Fatal(err)
	}
	if failedHistories != 2 {
		t.Fatalf("failed histories = %d, want 2", failedHistories)
	}

	houseIDs, err = service.Reconcile(fixture.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(houseIDs) != 0 {
		t.Fatalf("second reconcile affected houses = %v, want none", houseIDs)
	}
	failedHistories, err = fixture.db.Collection("ChoreStatusHistory").CountDocuments(
		fixture.ctx, bson.M{"status": entities.Failed},
	)
	if err != nil {
		t.Fatal(err)
	}
	if failedHistories != 2 {
		t.Fatalf("second reconcile created duplicate histories: %d", failedHistories)
	}
}

func assertChoreStatus(
	t *testing.T,
	fixture *concurrencyFixture,
	id primitive.ObjectID,
	wantStatus entities.ChoreStatus,
	wantCompleted bool,
) {
	t.Helper()
	var chore entities.Chore
	if err := fixture.db.Collection("Chore").FindOne(
		fixture.ctx, bson.M{"_id": id},
	).Decode(&chore); err != nil {
		t.Fatal(err)
	}
	if chore.Status != wantStatus || chore.IsCompleted != wantCompleted {
		t.Fatalf("chore %s status/completed = %d/%t, want %d/%t", id.Hex(), chore.Status, chore.IsCompleted, wantStatus, wantCompleted)
	}
	if wantStatus == entities.Failed && (chore.CompletedBy != "" || !chore.CompletedAt.IsZero()) {
		t.Fatalf("failed chore %s retained completion metadata: %+v", id.Hex(), chore)
	}
}
