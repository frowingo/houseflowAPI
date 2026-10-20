package tests

import (
	"net/http/httptest"
	"testing"
	"time"

	housecommands "houseflowApi/internal/application/house/commands"
	userCommands "houseflowApi/internal/application/user/commands"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// seedHouseData writes one document of every house-scoped kind. Validators are
// bypassed: only the house and chore links matter here.
func (f *concurrencyFixture) seedHouseData(t *testing.T, houseID string) {
	t.Helper()
	choreID := primitive.NewObjectID()
	insert := func(collection string, document bson.M) {
		if _, err := f.db.Collection(collection).InsertOne(f.ctx, document,
			options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal(err)
		}
	}
	insert("Chore", bson.M{"_id": choreID, "houseId": houseID})
	insert("ChoreStatusHistory", bson.M{"choreId": choreID.Hex()})
	for _, collection := range []string{
		"ChoreReviewVote", "Announcement", "Notification", "HouseInfoHistory", "GameSession", "GameMatchResult",
	} {
		// Unique ids keep the collections' unique indexes satisfied.
		unique := primitive.NewObjectID().Hex()
		insert(collection, bson.M{
			"houseId": houseID, "choreId": unique, "reviewerId": unique, "reviewRound": 1, "sessionId": unique,
		})
	}
}

func (f *concurrencyFixture) houseDataCount(t *testing.T, houseID primitive.ObjectID) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	count := func(collection string, filter bson.M) {
		value, err := f.db.Collection(collection).CountDocuments(f.ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		if value > 0 {
			counts[collection] = value
		}
	}
	count("House", bson.M{"_id": houseID})
	count("HouseInviteCode", bson.M{"_id": houseID})
	for _, collection := range []string{
		"Chore", "ChoreReviewVote", "Announcement", "Notification", "HouseInfoHistory", "GameSession", "GameMatchResult",
	} {
		count(collection, bson.M{"houseId": houseID.Hex()})
	}
	return counts
}

func TestDeleteSoleHouseMemberDeletesHouseData(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	soleHouse := fixture.createHouse(t, owner, 1)
	fixture.generateHouseInviteCode(t, soleHouse, owner)
	fixture.seedHouseData(t, soleHouse.Id.Hex())

	other := fixture.seedUser(t, "correct-password")
	otherHouse := fixture.createHouse(t, other, 1)
	fixture.seedHouseData(t, otherHouse.Id.Hex())

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: owner.Id.Hex()}); err != nil {
		t.Fatal(err)
	}

	if left := fixture.houseDataCount(t, soleHouse.Id); len(left) != 0 {
		t.Fatalf("data left for the deleted house: %v", left)
	}
	if history, err := fixture.db.Collection("ChoreStatusHistory").CountDocuments(fixture.ctx, bson.M{}); err != nil || history != 1 {
		t.Fatalf("chore status history=%d (err %v), want only the other house's entry", history, err)
	}
	if userHistory, err := fixture.db.Collection("UserInfoHistory").CountDocuments(fixture.ctx, bson.M{"userId": owner.Id.Hex()}); err != nil || userHistory != 0 {
		t.Fatalf("user info history=%d (err %v), want none", userHistory, err)
	}
	if users, err := fixture.db.Collection("User").CountDocuments(fixture.ctx, bson.M{"_id": owner.Id}); err != nil || users != 0 {
		t.Fatalf("user still stored (count %d, err %v)", users, err)
	}

	otherLeft := fixture.houseDataCount(t, otherHouse.Id)
	if len(otherLeft) != 8 {
		t.Fatalf("other house data=%v, want all of it kept", otherLeft)
	}
}

func TestDeleteUserLeavesSharedHouseToRemainingMember(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	member := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 2)
	code := fixture.generateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
		InviteCode: code, UserID: member.Id.Hex(),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.seedHouseData(t, house.Id.Hex())

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: owner.Id.Hex()}); err != nil {
		t.Fatal(err)
	}

	var stored entities.House
	if err := fixture.db.Collection("House").FindOne(fixture.ctx, bson.M{"_id": house.Id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.OwnerId != member.Id.Hex() || len(stored.MemberIds) != 1 || stored.MemberIds[0] != member.Id.Hex() {
		t.Fatalf("house=%+v, want the member as sole owner", stored)
	}
	if left := fixture.houseDataCount(t, house.Id); left["Chore"] != 1 || left["Announcement"] != 1 {
		t.Fatalf("shared house data=%v, want it kept", left)
	}
}

func TestDeleteMeDeletesTheSignedInUser(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	user := fixture.seedUser(t, "correct-password")
	other := fixture.seedUser(t, "correct-password")

	userController := controllers.NewUserController(fixture.sender, nil)
	app := fiber.New()
	signedIn := func(c *fiber.Ctx) error {
		c.Locals("userID", c.Get("X-Test-User"))
		return c.Next()
	}
	superAdminOnly := func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusForbidden) }
	// Same order as cmd/api/routes.go: "/me" must win over "/:id".
	app.Delete("/user/me", signedIn, userController.DeleteMe)
	app.Delete("/user/:id", signedIn, superAdminOnly, userController.DeleteUser)

	request := httptest.NewRequest(fiber.MethodDelete, "/user/me", nil)
	request.Header.Set("X-Test-User", user.Id.Hex())
	response, err := app.Test(request, int((10 * time.Second).Milliseconds()))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status=%d, want 204", response.StatusCode)
	}

	for id, wantCount := range map[primitive.ObjectID]int64{user.Id: 0, other.Id: 1} {
		count, err := fixture.db.Collection("User").CountDocuments(fixture.ctx, bson.M{"_id": id})
		if err != nil || count != wantCount {
			t.Fatalf("user %s count=%d (err %v), want %d", id.Hex(), count, err, wantCount)
		}
	}

	unauthenticated, err := app.Test(httptest.NewRequest(fiber.MethodDelete, "/user/me", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	if unauthenticated.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status without user=%d, want 401", unauthenticated.StatusCode)
	}
}
