package tests

import (
	"testing"

	housecommands "houseflowApi/internal/application/house/commands"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestUserHandlersCreateQueryListAndDeleteUser(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	email := "phase-three@example.test"

	created, err := cqrs.Send[*dtos.NewUserModel](fixture.ctx, fixture.sender, userCommands.CreateUserCommand{
		Firstname: "Phase", Lastname: "Three", Email: email, Password: "secure-password", Language: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != email {
		t.Fatalf("created email = %q, want %q", created.Email, email)
	}

	queried, err := cqrs.Send[*dtos.UserResultModel](fixture.ctx, fixture.sender, userQueries.GetUserByEmailQuery{Email: email})
	if err != nil {
		t.Fatal(err)
	}
	if queried.Firstname != "Phase" || queried.Lastname != "Three" {
		t.Fatalf("unexpected queried user: %+v", queried)
	}

	users, err := cqrs.Send[[]dtos.UserResultModel](fixture.ctx, fixture.sender, userQueries.ListUsersQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("user count = %d, want 1", len(users))
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: queried.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[*dtos.UserResultModel](fixture.ctx, fixture.sender, userQueries.GetUserByEmailQuery{Email: email}); err == nil {
		t.Fatal("deleted user is still queryable")
	}
}

func TestDeleteUserRemovesHouseMembership(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 2)
	member := fixture.seedUser(t, "correct-password")
	inviteCode := fixture.generateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: inviteCode,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: member.Id.Hex()}); err != nil {
		t.Fatal(err)
	}

	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"_id": member.Id}).Err(); err != mongo.ErrNoDocuments {
		t.Fatalf("deleted user lookup error = %v, want mongo.ErrNoDocuments", err)
	}
	var storedHouse entities.House
	if err := fixture.db.Collection("House").FindOne(fixture.ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
		t.Fatal(err)
	}
	if len(storedHouse.MemberIds) != 1 || storedHouse.MemberIds[0] != owner.Id.Hex() {
		t.Fatalf("house members after delete = %v, want only owner", storedHouse.MemberIds)
	}
}

func TestDeleteHouseOwnerReassignsOwnershipToRemainingMember(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 2)
	member := fixture.seedUser(t, "correct-password")
	inviteCode := fixture.generateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: inviteCode,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: owner.Id.Hex()}); err != nil {
		t.Fatal(err)
	}

	var storedHouse entities.House
	if err := fixture.db.Collection("House").FindOne(fixture.ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
		t.Fatal(err)
	}
	if storedHouse.OwnerId != member.Id.Hex() {
		t.Fatalf("house owner = %q, want %q", storedHouse.OwnerId, member.Id.Hex())
	}
	if len(storedHouse.MemberIds) != 1 || storedHouse.MemberIds[0] != member.Id.Hex() {
		t.Fatalf("house members after owner delete = %v, want only new owner", storedHouse.MemberIds)
	}
}

func TestDeleteSoleHouseOwnerIsRejected(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 1)

	_, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: owner.Id.Hex()})
	if !helpers.IsApplicationError(err, "user.error.cannot_delete_sole_house_owner") {
		t.Fatalf("delete error = %v, want sole-owner conflict", err)
	}

	var storedUser entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"_id": owner.Id}).Decode(&storedUser); err != nil {
		t.Fatalf("owner should remain after rejected delete: %v", err)
	}
	var storedHouse entities.House
	if err := fixture.db.Collection("House").FindOne(fixture.ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
		t.Fatal(err)
	}
	if storedHouse.OwnerId != owner.Id.Hex() || len(storedHouse.MemberIds) != 1 || storedHouse.MemberIds[0] != owner.Id.Hex() {
		t.Fatalf("house changed after rejected delete: %+v", storedHouse)
	}
}

func TestImageAssetUpdateInvalidatesCategoryCache(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	category := "avatar"
	publicID := "avatar_phase-three.png"

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, imageAssetCommands.CreateImageAssetCommand{
		Category: category, FileName: "phase-three.png", FileURL: "https://example.test/one.png", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	images, err := cqrs.Send[[]dtos.ImageAssetResultModel](fixture.ctx, fixture.sender, imageAssetQueries.GetImagesByCategoryQuery{
		Category: category,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].PublicID != publicID {
		t.Fatalf("unexpected cached image list: %+v", images)
	}

	isActive := false
	updatedURL := "https://example.test/two.png"
	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, imageAssetCommands.UpdateImageAssetCommand{
		PublicID: publicID, FileURL: &updatedURL, IsActive: &isActive,
	}); err != nil {
		t.Fatal(err)
	}

	images, err = cqrs.Send[[]dtos.ImageAssetResultModel](fixture.ctx, fixture.sender, imageAssetQueries.GetImagesByCategoryQuery{
		Category: category,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("inactive image remained in category cache: %+v", images)
	}
	if _, err := cqrs.Send[*dtos.ImageAssetResultModel](fixture.ctx, fixture.sender, imageAssetQueries.GetImageByPublicIDQuery{
		PublicID: publicID,
	}); err == nil {
		t.Fatal("inactive image is still publicly queryable")
	}
}
