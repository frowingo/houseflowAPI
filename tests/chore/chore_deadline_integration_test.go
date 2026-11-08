package chore

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
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestOverdueChoreCannotAdvanceBeforeDeadlineWorkerRuns(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	house := fixture.CreateHouse(t, owner, 1)
	created, err := cqrs.Send[*dtos.ChoreResponseModel](fixture.Ctx, fixture.Sender, appkit.CreateChoreCommand(dtos.CreateChoreModel{
		Title: "Overdue chore", Description: "Deadline command guard", AssignedTo: owner.Id.Hex(),
		DueDate: dtos.NewUTCDateTime(time.Now().Add(-time.Minute)), HouseId: house.Id.Hex(), Level: entities.Easy,
	}, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}

	result, err := cqrs.Send[[]dtos.ChoreResponseModel](fixture.Ctx, fixture.Sender, choreCommands.UpdateChoreStatusCommand{
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
	fixture := appkit.NewConcurrencyFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	dueDate := now.Add(-time.Minute)
	houseID := "deadline-house"
	chores := database.NewDbContext[entities.Chore](fixture.DB.Client(), fixture.DB.Name())
	histories := database.NewDbContext[entities.ChoreStatusHistory](fixture.DB.Client(), fixture.DB.Name())

	overdue, err := chores.Insert(fixture.Ctx, entities.Chore{
		Title: "Overdue", HouseId: houseID, DueDate: dueDate, Status: entities.Progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	earlyCompleted, err := chores.Insert(fixture.Ctx, entities.Chore{
		Title: "Early", HouseId: houseID, DueDate: dueDate, Status: entities.Completed,
		IsCompleted: true, CompletedAt: dueDate.Add(-time.Second), CompletedBy: "assignee",
	})
	if err != nil {
		t.Fatal(err)
	}
	lateCompleted, err := chores.Insert(fixture.Ctx, entities.Chore{
		Title: "Late", HouseId: houseID, DueDate: dueDate, Status: entities.Completed,
		IsCompleted: true, CompletedAt: dueDate, CompletedBy: "assignee",
	})
	if err != nil {
		t.Fatal(err)
	}
	future, err := chores.Insert(fixture.Ctx, entities.Chore{
		Title: "Future", HouseId: houseID, DueDate: now.Add(time.Minute), Status: entities.Progress,
	})
	if err != nil {
		t.Fatal(err)
	}

	service := choreApplication.NewDeadlineService(chores, histories, chorePolicies.NewWorkflowPolicy(), nil)
	houseIDs, err := service.Reconcile(fixture.Ctx, now)
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

	failedHistories, err := fixture.DB.Collection("ChoreStatusHistory").CountDocuments(
		fixture.Ctx, bson.M{"status": entities.Failed},
	)
	if err != nil {
		t.Fatal(err)
	}
	if failedHistories != 2 {
		t.Fatalf("failed histories = %d, want 2", failedHistories)
	}

	houseIDs, err = service.Reconcile(fixture.Ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(houseIDs) != 0 {
		t.Fatalf("second reconcile affected houses = %v, want none", houseIDs)
	}
	failedHistories, err = fixture.DB.Collection("ChoreStatusHistory").CountDocuments(
		fixture.Ctx, bson.M{"status": entities.Failed},
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
	fixture *appkit.ConcurrencyFixture,
	id primitive.ObjectID,
	wantStatus entities.ChoreStatus,
	wantCompleted bool,
) {
	t.Helper()
	var chore entities.Chore
	if err := fixture.DB.Collection("Chore").FindOne(
		fixture.Ctx, bson.M{"_id": id},
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
