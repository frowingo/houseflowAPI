package chore

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"houseflowApi/external/validator"
	choreApplication "houseflowApi/internal/application/chore"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	choreQueries "houseflowApi/internal/application/chore/queries"
	housequeries "houseflowApi/internal/application/house/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/appkit"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestCreateChoreModelRequiresDueDate(t *testing.T) {
	model := dtos.CreateChoreModel{
		Title: "Dishes", Description: "Wash the dishes", AssignedTo: "507f1f77bcf86cd799439011",
		HouseId: "507f1f77bcf86cd799439012", Level: entities.Easy,
	}
	if err := validator.NewValidator().Validate(model); err == nil {
		t.Fatal("chore without dueDate passed validation")
	}

	var fromJSON dtos.CreateChoreModel
	if err := json.Unmarshal([]byte(`{"dueDate":null}`), &fromJSON); err != nil {
		t.Fatal(err)
	}
	model.DueDate = fromJSON.DueDate
	if err := validator.NewValidator().Validate(model); err == nil {
		t.Fatal("chore with null dueDate passed validation")
	}

	model.DueDate = dtos.NewUTCDateTime(time.Now().Add(time.Hour))
	if err := validator.NewValidator().Validate(model); err != nil {
		t.Fatalf("chore with dueDate failed validation: %v", err)
	}
}

// Failed chores an older release wrote without a close time get one from
// the daily pass: the failed history when it is before the due date, the
// due date otherwise.
func TestChoreDeadlineFullPassDatesUndatedFailures(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	chores := database.NewDbContext[entities.Chore](fixture.DB.Client(), fixture.DB.Name())
	histories := database.NewDbContext[entities.ChoreStatusHistory](fixture.DB.Client(), fixture.DB.Name())
	reviewFailedAt := now.Add(-3 * time.Hour)
	dueDate := now.Add(-time.Hour)

	reviewFailure := insertChore(t, chores, entities.Chore{HouseId: "heal-house", Status: entities.Failed, DueDate: dueDate})
	insertHistory(t, histories, reviewFailure.Id, reviewFailedAt)
	deadlineFailure := insertChore(t, chores, entities.Chore{HouseId: "heal-house", Status: entities.Failed, DueDate: dueDate})
	insertHistory(t, histories, deadlineFailure.Id, now)
	noHistory := insertChore(t, chores, entities.Chore{HouseId: "heal-house", Status: entities.Failed, DueDate: dueDate})

	service := choreApplication.NewDeadlineService(chores, histories, chorePolicies.NewWorkflowPolicy(), nil)
	if _, err := service.Reconcile(fixture.Ctx, now); err != nil {
		t.Fatal(err)
	}

	assertClosed(t, fixture, reviewFailure.Id, reviewFailedAt)
	assertClosed(t, fixture, deadlineFailure.Id, dueDate)
	assertClosed(t, fixture, noHistory.Id, dueDate)
}

func TestChoreArchiveMigrationDatesFailedChores(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	chores := database.NewDbContext[entities.Chore](fixture.DB.Client(), fixture.DB.Name())
	histories := database.NewDbContext[entities.ChoreStatusHistory](fixture.DB.Client(), fixture.DB.Name())
	dueDate := now.Add(-time.Hour)
	reviewFailedAt := now.Add(-2 * time.Hour)

	reviewFailure := insertChore(t, chores, entities.Chore{HouseId: "migration-house", Status: entities.Failed, DueDate: dueDate})
	insertHistory(t, histories, reviewFailure.Id, reviewFailedAt)
	deadlineFailure := insertChore(t, chores, entities.Chore{HouseId: "migration-house", Status: entities.Failed, DueDate: dueDate})
	insertHistory(t, histories, deadlineFailure.Id, now)
	completedAt := now.Add(-4 * time.Hour)
	completed := insertChore(t, chores, entities.Chore{
		HouseId: "migration-house", Status: entities.Completed, IsCompleted: true,
		CompletedAt: completedAt, CompletedBy: "assignee", DueDate: dueDate,
	})

	var archive migrations.Migration
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0071" {
			archive = migration
		}
	}
	if archive == nil {
		t.Fatal("migration 0071 is not registered")
	}
	if err := archive.Up(fixture.Ctx, fixture.DB); err != nil {
		t.Fatal(err)
	}

	assertClosed(t, fixture, reviewFailure.Id, reviewFailedAt)
	assertClosed(t, fixture, deadlineFailure.Id, dueDate)
	stored := findChore(t, fixture, completed.Id)
	if !stored.CompletedAt.Equal(completedAt) || stored.CompletedBy != "assignee" {
		t.Fatalf("completed chore changed: %+v", stored)
	}
}

func TestHouseDetailsReturnsOpenAndRecentlyClosedChores(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	house := fixture.CreateHouse(t, owner, 2)
	chores := database.NewDbContext[entities.Chore](fixture.DB.Client(), fixture.DB.Name())
	now := time.Now().UTC()
	houseID := house.Id.Hex()

	want := map[primitive.ObjectID]string{}
	for _, status := range []entities.ChoreStatus{entities.Draft, entities.Progress, entities.InTest} {
		chore := insertChore(t, chores, entities.Chore{
			Title: "Open", HouseId: houseID, Status: status, DueDate: now.Add(-30 * 24 * time.Hour),
		})
		want[chore.Id] = "open"
	}
	recentCompleted := insertChore(t, chores, entities.Chore{
		Title: "Recent completed", HouseId: houseID, Status: entities.Completed, IsCompleted: true,
		CompletedAt: now.Add(-time.Hour),
	})
	want[recentCompleted.Id] = "recent completed"
	recentFailed := insertChore(t, chores, entities.Chore{
		Title: "Recent failed", HouseId: houseID, Status: entities.Failed,
		CompletedAt: now.Add(-47 * time.Hour), CompletedBy: entities.ChoreSystemActor,
	})
	want[recentFailed.Id] = "recent failed"
	insertChore(t, chores, entities.Chore{
		Title: "Old completed", HouseId: houseID, Status: entities.Completed, IsCompleted: true,
		CompletedAt: now.Add(-49 * time.Hour),
	})
	insertChore(t, chores, entities.Chore{
		Title: "Old failed", HouseId: houseID, Status: entities.Failed,
		CompletedAt: now.Add(-72 * time.Hour), CompletedBy: entities.ChoreSystemActor,
	})
	insertChore(t, chores, entities.Chore{Title: "Undated failed", HouseId: houseID, Status: entities.Failed})
	insertChore(t, chores, entities.Chore{Title: "Other house", HouseId: "other-house", Status: entities.Draft})

	details, err := cqrs.Send[*dtos.HouseDetailsModel](fixture.Ctx, fixture.Sender, housequeries.GetHouseDetailsQuery{
		HouseID: houseID, RequesterID: owner.Id.Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Chores) != len(want) {
		t.Fatalf("details chores = %d, want %d: %+v", len(details.Chores), len(want), details.Chores)
	}
	for _, chore := range details.Chores {
		id, _ := primitive.ObjectIDFromHex(chore.Id)
		if _, ok := want[id]; !ok {
			t.Fatalf("details returned unexpected chore %q", chore.Title)
		}
	}
}

func TestChoreArchivePagesClosedChoresInRangeOverHTTP(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	house := fixture.CreateHouse(t, owner, 2)
	chores := database.NewDbContext[entities.Chore](fixture.DB.Client(), fixture.DB.Name())
	houseID := house.Id.Hex()
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	inRange := map[string]bool{}
	for index := 0; index < 23; index++ {
		status := entities.Completed
		if index%3 == 0 {
			status = entities.Failed
		}
		// Pairs share a completedAt so paging must break ties by _id.
		completedAt := from.Add(time.Duration(index/2) * time.Hour)
		chore := insertChore(t, chores, entities.Chore{
			Title: "Closed", HouseId: houseID, Status: status, CompletedAt: completedAt,
		})
		inRange[chore.Id.Hex()] = true
	}
	insertChore(t, chores, entities.Chore{Title: "Before", HouseId: houseID, Status: entities.Completed, CompletedAt: from.Add(-time.Millisecond)})
	insertChore(t, chores, entities.Chore{Title: "At end", HouseId: houseID, Status: entities.Completed, CompletedAt: to})
	insertChore(t, chores, entities.Chore{Title: "Open", HouseId: houseID, Status: entities.Progress})
	insertChore(t, chores, entities.Chore{Title: "Other house", HouseId: "other-house", Status: entities.Completed, CompletedAt: from})

	app := fiber.New()
	controller := controllers.NewChoreController(fixture.Sender, nil)
	app.Get("/api/v1/chore/archive", func(c *fiber.Ctx) error {
		c.Locals("userID", owner.Id.Hex())
		return c.Next()
	}, controller.GetChoreArchive)

	query := url.Values{}
	query.Set("houseId", houseID)
	query.Set("from", "2026-11-01T03:00:00+03:00")
	query.Set("to", to.Format(time.RFC3339))
	next := "/api/v1/chore/archive?" + query.Encode()

	seen := map[string]bool{}
	var pageSizes []int
	var previous time.Time
	for next != "" {
		response, err := app.Test(httptest.NewRequest(fiber.MethodGet, next, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusOK {
			t.Fatalf("GET %s status = %d", next, response.StatusCode)
		}
		var body struct {
			Data struct {
				Chores  []dtos.ChoreResponseModel `json:"chores"`
				NextUrl *string                   `json:"nextUrl"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		pageSizes = append(pageSizes, len(body.Data.Chores))
		for _, chore := range body.Data.Chores {
			if !inRange[chore.Id] || seen[chore.Id] {
				t.Fatalf("unexpected or repeated chore %s (%s)", chore.Id, chore.Title)
			}
			seen[chore.Id] = true
			if !previous.IsZero() && chore.CompletedAt.After(previous) {
				t.Fatalf("archive is not newest first: %v after %v", chore.CompletedAt.Time, previous)
			}
			previous = chore.CompletedAt.Time
		}
		next = ""
		if body.Data.NextUrl != nil {
			next = *body.Data.NextUrl
		}
	}
	if len(pageSizes) != 3 || pageSizes[0] != 10 || pageSizes[1] != 10 || pageSizes[2] != 3 {
		t.Fatalf("page sizes = %v, want [10 10 3]", pageSizes)
	}
	if len(seen) != len(inRange) {
		t.Fatalf("archive listed %d chores, want %d", len(seen), len(inRange))
	}
}

func TestChoreArchiveRejectsBadInput(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	outsider := fixture.SeedUser(t, "correct-password")
	house := fixture.CreateHouse(t, owner, 2)
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	for name, query := range map[string]choreQueries.GetChoreArchiveQuery{
		"reversed range": {HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), From: to, To: from},
		"empty range":    {HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), From: from, To: from},
		"missing from":   {HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), To: to},
		"bad cursor":     {HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), From: from, To: to, Cursor: "not-a-cursor"},
		"invalid house":  {HouseID: "house", RequesterID: owner.Id.Hex(), From: from, To: to},
		"not a member":   {HouseID: house.Id.Hex(), RequesterID: outsider.Id.Hex(), From: from, To: to},
	} {
		if _, err := cqrs.Send[*dtos.ChoreArchivePageModel](fixture.Ctx, fixture.Sender, query); err == nil {
			t.Fatalf("%s: archive query succeeded", name)
		}
	}

	page, err := cqrs.Send[*dtos.ChoreArchivePageModel](fixture.Ctx, fixture.Sender, choreQueries.GetChoreArchiveQuery{
		HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), From: from, To: to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Chores) != 0 || page.NextCursor != "" {
		t.Fatalf("empty archive page = %+v", page)
	}
}

func insertChore(t *testing.T, chores *database.DbContext[entities.Chore], chore entities.Chore) *entities.Chore {
	t.Helper()
	inserted, err := chores.Insert(t.Context(), chore)
	if err != nil {
		t.Fatal(err)
	}
	return inserted
}

func insertHistory(
	t *testing.T,
	histories *database.DbContext[entities.ChoreStatusHistory],
	choreID primitive.ObjectID,
	at time.Time,
) {
	t.Helper()
	if _, err := histories.Insert(t.Context(), entities.ChoreStatusHistory{
		ChoreId: choreID.Hex(), Status: entities.Failed, DateTime: at, Updater: entities.ChoreSystemActor,
	}); err != nil {
		t.Fatal(err)
	}
}

func findChore(t *testing.T, fixture *appkit.ConcurrencyFixture, id primitive.ObjectID) entities.Chore {
	t.Helper()
	var chore entities.Chore
	if err := fixture.DB.Collection("Chore").FindOne(fixture.Ctx, bson.M{"_id": id}).Decode(&chore); err != nil {
		t.Fatal(err)
	}
	return chore
}

func assertClosed(t *testing.T, fixture *appkit.ConcurrencyFixture, id primitive.ObjectID, want time.Time) {
	t.Helper()
	chore := findChore(t, fixture, id)
	if chore.Status != entities.Failed || chore.IsCompleted ||
		chore.CompletedBy != entities.ChoreSystemActor || !chore.CompletedAt.Equal(want) {
		t.Fatalf("chore %s = %d/%t/%q/%v, want failed/false/system/%v",
			id.Hex(), chore.Status, chore.IsCompleted, chore.CompletedBy, chore.CompletedAt, want)
	}
}
