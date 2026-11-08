package account

import (
	"net/http/httptest"
	"testing"
	"time"

	housecommands "houseflowApi/internal/application/house/commands"
	userCommands "houseflowApi/internal/application/user/commands"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/tests/testkit/appkit"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestDeleteSoleHouseMemberDeletesHouseData(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	soleHouse := fixture.CreateHouse(t, owner, 1)
	fixture.GenerateHouseInviteCode(t, soleHouse, owner)
	fixture.SeedHouseData(t, soleHouse.Id.Hex())

	other := fixture.SeedUser(t, "correct-password")
	otherHouse := fixture.CreateHouse(t, other, 1)
	fixture.SeedHouseData(t, otherHouse.Id.Hex())

	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, userCommands.DeleteUserCommand{UserID: owner.Id.Hex()}); err != nil {
		t.Fatal(err)
	}

	if left := fixture.HouseDataCount(t, soleHouse.Id); len(left) != 0 {
		t.Fatalf("data left for the deleted house: %v", left)
	}
	if history, err := fixture.DB.Collection("ChoreStatusHistory").CountDocuments(fixture.Ctx, bson.M{}); err != nil || history != 1 {
		t.Fatalf("chore status history=%d (err %v), want only the other house's entry", history, err)
	}
	if userHistory, err := fixture.DB.Collection("UserInfoHistory").CountDocuments(fixture.Ctx, bson.M{"userId": owner.Id.Hex()}); err != nil || userHistory != 0 {
		t.Fatalf("user info history=%d (err %v), want none", userHistory, err)
	}
	if users, err := fixture.DB.Collection("User").CountDocuments(fixture.Ctx, bson.M{"_id": owner.Id}); err != nil || users != 0 {
		t.Fatalf("user still stored (count %d, err %v)", users, err)
	}

	otherLeft := fixture.HouseDataCount(t, otherHouse.Id)
	if len(otherLeft) != 8 {
		t.Fatalf("other house data=%v, want all of it kept", otherLeft)
	}
}

func TestDeleteUserLeavesSharedHouseToRemainingMember(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	member := fixture.SeedUser(t, "correct-password")
	house := fixture.CreateHouse(t, owner, 2)
	code := fixture.GenerateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](fixture.Ctx, fixture.Sender, housecommands.JoinHouseCommand{
		InviteCode: code, UserID: member.Id.Hex(),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.SeedHouseData(t, house.Id.Hex())

	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, userCommands.DeleteUserCommand{UserID: owner.Id.Hex()}); err != nil {
		t.Fatal(err)
	}

	var stored entities.House
	if err := fixture.DB.Collection("House").FindOne(fixture.Ctx, bson.M{"_id": house.Id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.OwnerId != member.Id.Hex() || len(stored.MemberIds) != 1 || stored.MemberIds[0] != member.Id.Hex() {
		t.Fatalf("house=%+v, want the member as sole owner", stored)
	}
	if left := fixture.HouseDataCount(t, house.Id); left["Chore"] != 1 || left["Announcement"] != 1 {
		t.Fatalf("shared house data=%v, want it kept", left)
	}
}

func TestDeleteMeDeletesTheSignedInUser(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	user := fixture.SeedUser(t, "correct-password")
	other := fixture.SeedUser(t, "correct-password")

	userController := controllers.NewUserController(fixture.Sender, nil)
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
		count, err := fixture.DB.Collection("User").CountDocuments(fixture.Ctx, bson.M{"_id": id})
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
