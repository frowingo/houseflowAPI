package tests

import (
	"strings"
	"testing"
	"time"

	housecommands "houseflowApi/internal/application/house/commands"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

func TestHouseInviteCodeCanBeReusedUntilItExpires(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 3)
	firstMember := fixture.seedUser(t, "correct-password")
	secondMember := fixture.seedUser(t, "correct-password")

	response, err := cqrs.Send[*dtos.HouseInviteCodeResponseModel](fixture.ctx, fixture.sender, housecommands.GenerateHouseInviteCodeCommand{
		HouseID: house.Id.Hex(), UserID: owner.Id.Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.InviteCode) != 8 || response.ExpiresInSeconds != 120 {
		t.Fatalf("unexpected invite response: %+v", response)
	}

	var storedInvite entities.HouseInviteCode
	if err := fixture.db.Collection("HouseInviteCode").FindOne(fixture.ctx, bson.M{"_id": house.Id}).Decode(&storedInvite); err != nil {
		t.Fatal(err)
	}
	if storedInvite.CodeDigest == response.InviteCode || storedInvite.CodeDigest == "" {
		t.Fatal("raw invite code was stored instead of its digest")
	}

	for _, member := range []entities.User{firstMember, secondMember} {
		if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
			UserID: member.Id.Hex(), InviteCode: strings.ToLower(response.InviteCode),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneratingANewHouseInviteCodeInvalidatesThePreviousCode(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 2)
	member := fixture.seedUser(t, "correct-password")

	oldCode := fixture.generateHouseInviteCode(t, house, owner)
	newCode := fixture.generateHouseInviteCode(t, house, owner)
	if oldCode == newCode {
		t.Skip("secure random generator produced the same invite code twice")
	}

	if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: oldCode,
	}); !helpers.IsApplicationError(err, "house.error.invalid_or_expired_invite_code") {
		t.Fatalf("old invite code error = %v, want invalid or expired", err)
	}

	if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: newCode,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredHouseInviteCodeAndNonOwnerGenerationAreRejected(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, owner, 2)
	member := fixture.seedUser(t, "correct-password")

	if _, err := cqrs.Send[*dtos.HouseInviteCodeResponseModel](fixture.ctx, fixture.sender, housecommands.GenerateHouseInviteCodeCommand{
		HouseID: house.Id.Hex(), UserID: member.Id.Hex(),
	}); !helpers.IsApplicationError(err, "house.error.only_owner_can_generate_invite_code") {
		t.Fatalf("non-owner generation error = %v, want owner-only error", err)
	}

	inviteCode := fixture.generateHouseInviteCode(t, house, owner)
	if _, err := fixture.db.Collection("HouseInviteCode").UpdateOne(fixture.ctx,
		bson.M{"_id": house.Id}, bson.M{"$set": bson.M{"expiresAt": time.Now().Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}

	if _, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: inviteCode,
	}); !helpers.IsApplicationError(err, "house.error.invalid_or_expired_invite_code") {
		t.Fatalf("expired invite code error = %v, want invalid or expired", err)
	}
}
