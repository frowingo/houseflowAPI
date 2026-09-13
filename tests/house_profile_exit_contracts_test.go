package tests

import (
	"testing"
	"time"

	housecommands "houseflowApi/internal/application/house/commands"
	housequeries "houseflowApi/internal/application/house/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestHouseInfoHistoryContractAndMigration(t *testing.T) {
	history := entities.HouseInfoHistory{
		Id: primitive.NewObjectID(), HouseId: primitive.NewObjectID().Hex(),
		ColumnName: entities.HouseInfoColumnName, Value: "New name",
		UpdatedBy: primitive.NewObjectID().Hex(), UpdateOn: time.Now().UTC(),
	}
	payload, err := bson.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var document bson.M
	if err := bson.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"houseId", "columnName", "value", "updatedBy", "updateOn"} {
		if _, exists := document[field]; !exists {
			t.Fatalf("house history document does not contain %q: %#v", field, document)
		}
	}

	registered := false
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0036" {
			registered = true
			break
		}
	}
	if !registered {
		t.Fatal("migration 0036 is not registered")
	}
}

func TestGetHouseInfosReturnsMemberProfileAndRequiresMembership(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	member := f.seedUser(t, "correct-password")
	outsider := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 3)
	joinHouseForTest(t, f, house, owner, member)

	infos, err := cqrs.Send[*dtos.HouseInfosResponseModel](f.ctx, f.sender, housequeries.GetHouseInfosQuery{
		HouseID: house.Id.Hex(), RequesterID: member.Id.Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if infos.HouseName != house.Name || infos.HouseMemberCount != 2 || infos.HouseMemberCountLimit != 3 {
		t.Fatalf("unexpected house infos: %+v", infos)
	}
	if len(infos.HouseMembers) != 2 || infos.HouseMembers[0].UserId != owner.Id.Hex() || !infos.HouseMembers[0].IsOwner {
		t.Fatalf("unexpected members: %+v", infos.HouseMembers)
	}
	if infos.HouseMembers[1].UserId != member.Id.Hex() || infos.HouseMembers[1].IsOwner {
		t.Fatalf("unexpected member role: %+v", infos.HouseMembers[1])
	}

	_, err = cqrs.Send[*dtos.HouseInfosResponseModel](f.ctx, f.sender, housequeries.GetHouseInfosQuery{
		HouseID: house.Id.Hex(), RequesterID: outsider.Id.Hex(),
	})
	if !helpers.IsApplicationError(err, "house.error.user_not_member") {
		t.Fatalf("non-member error = %v", err)
	}
}

func TestUpdateHouseProfileTracksEachChangedFieldFor48Hours(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	member := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 4)
	joinHouseForTest(t, f, house, owner, member)

	name := "Updated house"
	image := "https://example.test/house.png"
	limit := 5
	houseType := entities.DormRoom
	infos, err := cqrs.Send[*dtos.HouseInfosResponseModel](f.ctx, f.sender, housecommands.UpdateHouseProfileCommand{
		HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), HouseName: &name,
		HouseProfileImage: &image, HouseMemberCountLimit: &limit, HouseType: &houseType,
	})
	if err != nil {
		t.Fatal(err)
	}
	if infos.HouseName != name || infos.HouseProfileImage != image || infos.HouseMemberCountLimit != limit || infos.HouseType != houseType {
		t.Fatalf("unexpected updated infos: %+v", infos)
	}

	historyCount, err := f.db.Collection("HouseInfoHistory").CountDocuments(f.ctx, bson.M{"houseId": house.Id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if historyCount != 4 {
		t.Fatalf("history count = %d, want 4", historyCount)
	}

	secondName := "Another name"
	_, err = cqrs.Send[*dtos.HouseInfosResponseModel](f.ctx, f.sender, housecommands.UpdateHouseProfileCommand{
		HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), HouseName: &secondName,
	})
	if !helpers.IsApplicationError(err, "house.error.profile_field_update_limit") {
		t.Fatalf("second name update error = %v", err)
	}

	newLimit := 1
	_, err = cqrs.Send[*dtos.HouseInfosResponseModel](f.ctx, f.sender, housecommands.UpdateHouseProfileCommand{
		HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(), HouseMemberCountLimit: &newLimit,
	})
	if !helpers.IsApplicationError(err, "house.error.member_limit_below_current") {
		t.Fatalf("limit below member count error = %v", err)
	}

	memberName := "Member cannot edit"
	_, err = cqrs.Send[*dtos.HouseInfosResponseModel](f.ctx, f.sender, housecommands.UpdateHouseProfileCommand{
		HouseID: house.Id.Hex(), RequesterID: member.Id.Hex(), HouseName: &memberName,
	})
	if !helpers.IsApplicationError(err, "house.error.owner_required") {
		t.Fatalf("non-owner update error = %v", err)
	}
}

func TestHouseExitSupportsSelfExitOwnerRemovalAndOwnershipTransfer(t *testing.T) {
	t.Run("member cannot remove another member", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		firstMember := f.seedUser(t, "correct-password")
		secondMember := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 3)
		joinHouseForTest(t, f, house, owner, firstMember)
		joinHouseForTest(t, f, house, owner, secondMember)

		_, err := cqrs.Send[cqrs.NoResult](f.ctx, f.sender, housecommands.ExitHouseCommand{
			HouseID: house.Id.Hex(), TargetUserID: secondMember.Id.Hex(), RequesterID: firstMember.Id.Hex(),
		})
		if !helpers.IsApplicationError(err, "house.error.cannot_remove_member") {
			t.Fatalf("non-owner removal error = %v", err)
		}
	})

	t.Run("member exits", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		member := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 2)
		joinHouseForTest(t, f, house, owner, member)

		if _, err := cqrs.Send[cqrs.NoResult](f.ctx, f.sender, housecommands.ExitHouseCommand{
			HouseID: house.Id.Hex(), TargetUserID: member.Id.Hex(), RequesterID: member.Id.Hex(),
		}); err != nil {
			t.Fatal(err)
		}
		assertMembershipRemoved(t, f, house.Id.Hex(), member.Id.Hex())
	})

	t.Run("owner removes member and invalidates invite", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		member := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 2)
		joinHouseForTest(t, f, house, owner, member)
		f.generateHouseInviteCode(t, house, owner)

		if _, err := cqrs.Send[cqrs.NoResult](f.ctx, f.sender, housecommands.ExitHouseCommand{
			HouseID: house.Id.Hex(), TargetUserID: member.Id.Hex(), RequesterID: owner.Id.Hex(),
		}); err != nil {
			t.Fatal(err)
		}
		assertMembershipRemoved(t, f, house.Id.Hex(), member.Id.Hex())
		inviteCount, err := f.db.Collection("HouseInviteCode").CountDocuments(f.ctx, bson.M{"_id": house.Id})
		if err != nil {
			t.Fatal(err)
		}
		if inviteCount != 0 {
			t.Fatalf("active invite count = %d, want 0", inviteCount)
		}
	})

	t.Run("owner exits and transfers ownership", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		member := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 2)
		joinHouseForTest(t, f, house, owner, member)

		if _, err := cqrs.Send[cqrs.NoResult](f.ctx, f.sender, housecommands.ExitHouseCommand{
			HouseID: house.Id.Hex(), TargetUserID: owner.Id.Hex(), RequesterID: owner.Id.Hex(),
		}); err != nil {
			t.Fatal(err)
		}
		var storedHouse entities.House
		if err := f.db.Collection("House").FindOne(f.ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
			t.Fatal(err)
		}
		if storedHouse.OwnerId != member.Id.Hex() {
			t.Fatalf("ownerId = %s, want %s", storedHouse.OwnerId, member.Id.Hex())
		}
		assertMembershipRemoved(t, f, house.Id.Hex(), owner.Id.Hex())
	})

	t.Run("sole owner cannot exit", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 1)
		_, err := cqrs.Send[cqrs.NoResult](f.ctx, f.sender, housecommands.ExitHouseCommand{
			HouseID: house.Id.Hex(), TargetUserID: owner.Id.Hex(), RequesterID: owner.Id.Hex(),
		})
		if !helpers.IsApplicationError(err, "house.error.sole_owner_cannot_exit") {
			t.Fatalf("sole-owner exit error = %v", err)
		}
	})
}

func joinHouseForTest(t *testing.T, f *concurrencyFixture, house *entities.House, owner, member entities.User) {
	t.Helper()
	inviteCode := f.generateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: inviteCode,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertMembershipRemoved(t *testing.T, f *concurrencyFixture, houseID, userID string) {
	t.Helper()
	houseObjectID, _ := helpers.ToMongoId(houseID)
	userObjectID, _ := helpers.ToMongoId(userID)
	var house entities.House
	if err := f.db.Collection("House").FindOne(f.ctx, bson.M{"_id": houseObjectID}).Decode(&house); err != nil {
		t.Fatal(err)
	}
	if housePoliciesContains(house.MemberIds, userID) {
		t.Fatalf("user %s still exists in house members", userID)
	}
	var user entities.User
	if err := f.db.Collection("User").FindOne(f.ctx, bson.M{"_id": userObjectID}).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if housePoliciesContains(user.HouseIds, houseID) {
		t.Fatalf("house %s still exists in user memberships", houseID)
	}
}

func housePoliciesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
