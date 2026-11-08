package house

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	authCommands "houseflowApi/internal/application/auth/commands"
	choreCommands "houseflowApi/internal/application/chore/commands"
	choreQueries "houseflowApi/internal/application/chore/queries"
	housecommands "houseflowApi/internal/application/house/commands"
	housequeries "houseflowApi/internal/application/house/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func updateChoreStatusCommand(model dtos.BulkUpdateChoreStatusModel, userID string) choreCommands.UpdateChoreStatusCommand {
	updates := make([]choreCommands.ChoreStatusUpdate, 0, len(model.Chores))
	for _, update := range model.Chores {
		updates = append(updates, choreCommands.ChoreStatusUpdate{ChoreID: update.ChoreId, Status: update.Status})
	}
	return choreCommands.UpdateChoreStatusCommand{HouseID: model.HouseId, Chores: updates, UserID: userID}
}

func TestGetHouseDetailsQueryReturnsCompleteSnapshot(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	house := f.CreateHouse(t, owner, 2)
	member := f.SeedUser(t, "correct-password")
	inviteCode := f.GenerateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: inviteCode,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[*dtos.AnnouncementResponseModel](f.Ctx, f.Sender, housecommands.CreateAnnouncementCommand{
		HouseID: house.Id.Hex(), UserID: owner.Id.Hex(), Title: "Snapshot notice", Description: "Snapshot test",
	}); err != nil {
		t.Fatal(err)
	}
	createdChore, err := cqrs.Send[*dtos.ChoreResponseModel](f.Ctx, f.Sender, appkit.CreateChoreCommand(dtos.CreateChoreModel{
		Title:       "Snapshot chore",
		Description: "Snapshot test chore",
		AssignedTo:  owner.Id.Hex(),
		DueDate:     dtos.NewUTCDateTime(time.Now().Add(time.Hour)),
		HouseId:     house.Id.Hex(),
		Level:       entities.Easy,
	}, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	if len(createdChore.StatusHistories) != 1 || createdChore.StatusHistories[0].Status != entities.Draft {
		t.Fatalf("created chore histories = %+v, want one draft history", createdChore.StatusHistories)
	}
	if len(createdChore.ReviewVotes) != 0 {
		t.Fatalf("created chore review votes = %+v, want none", createdChore.ReviewVotes)
	}

	details, err := cqrs.Send[*dtos.HouseDetailsModel](f.Ctx, f.Sender, housequeries.GetHouseDetailsQuery{
		HouseID: house.Id.Hex(), RequesterID: owner.Id.Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Members) != 2 || len(details.Chores) != 1 || len(details.Announcements) != 1 {
		t.Fatalf("members=%d chores=%d announcements=%d, want 2, 1, 1",
			len(details.Members), len(details.Chores), len(details.Announcements))
	}
	if details.Announcements[0].AnnouncedBy != "Test User" {
		t.Fatalf("announcedBy = %q, want %q", details.Announcements[0].AnnouncedBy, "Test User")
	}
}

func TestConcurrentHouseJoinsRespectCapacityAndKeepMembershipConsistent(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	house := f.CreateHouse(t, owner, 5)
	inviteCode := f.GenerateHouseInviteCode(t, house, owner)
	users := make([]entities.User, 16)
	for i := range users {
		users[i] = f.SeedUser(t, "correct-password")
	}

	errs := make([]error, len(users))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range users {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, errs[i] = cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.JoinHouseCommand{
				UserID: users[i].Id.Hex(), InviteCode: inviteCode,
			})
		}(i)
	}
	close(start)
	workers.Wait()

	var storedHouse entities.House
	if err := f.DB.Collection("House").FindOne(f.Ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
		t.Fatal(err)
	}
	if len(storedHouse.MemberIds) != 5 {
		t.Fatalf("member count = %d, want 5", len(storedHouse.MemberIds))
	}
	members := make(map[string]bool, len(storedHouse.MemberIds))
	for _, id := range storedHouse.MemberIds {
		if members[id] {
			t.Fatalf("duplicate house member %s", id)
		}
		members[id] = true
	}
	successes := 0
	for i, joinErr := range errs {
		if joinErr == nil {
			successes++
		}
		var storedUser entities.User
		if err := f.DB.Collection("User").FindOne(f.Ctx, bson.M{"_id": users[i].Id}).Decode(&storedUser); err != nil {
			t.Fatal(err)
		}
		joined := members[users[i].Id.Hex()]
		if joined != (joinErr == nil) || len(storedUser.HouseIds) != boolInt(joined) {
			t.Fatalf("divergent membership for %s: house=%v user=%v err=%v", users[i].Id.Hex(), joined, storedUser.HouseIds, joinErr)
		}
	}
	if successes != 4 {
		t.Fatalf("successful joins = %d, want 4", successes)
	}
}

func TestConcurrentHouseCreatesDoNotLoseUserMemberships(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	errs := make([]error, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range errs {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, errs[i] = cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.CreateHouseCommand{
				OwnerID: owner.Id.Hex(), Name: fmt.Sprintf("House %d", i),
				Type: entities.SharedHouse, MaxMemberCount: 4,
			})
		}(i)
	}
	close(start)
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent house create failed: %v", err)
		}
	}
	var storedUser entities.User
	if err := f.DB.Collection("User").FindOne(f.Ctx, bson.M{"_id": owner.Id}).Decode(&storedUser); err != nil {
		t.Fatal(err)
	}
	houses, err := f.DB.Collection("House").CountDocuments(f.Ctx, bson.M{"ownerId": owner.Id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if houses != 8 || len(storedUser.HouseIds) != 8 {
		t.Fatalf("houses=%d user.houseIds=%d, want 8 and 8", houses, len(storedUser.HouseIds))
	}
}

func TestChoreBulkRollbackAndConcurrentTransition(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	house := f.CreateHouse(t, owner, 2)
	member := f.SeedUser(t, "correct-password")
	inviteCode := f.GenerateHouseInviteCode(t, house, owner)
	if _, err := cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.JoinHouseCommand{UserID: member.Id.Hex(), InviteCode: inviteCode}); err != nil {
		t.Fatal(err)
	}
	create := func(title string) *dtos.ChoreResponseModel {
		result, err := cqrs.Send[*dtos.ChoreResponseModel](f.Ctx, f.Sender, appkit.CreateChoreCommand(dtos.CreateChoreModel{
			Title: title, Description: "Concurrency test chore", AssignedTo: owner.Id.Hex(),
			DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Easy,
		}, owner.Id.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := create("First chore")
	second := create("Second chore")
	_, err := cqrs.Send[[]dtos.ChoreResponseModel](f.Ctx, f.Sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
		HouseId: house.Id.Hex(),
		Chores: []dtos.UpdateChoreStatusModel{
			{ChoreId: first.Id, Status: entities.Progress},
			{ChoreId: second.Id, Status: entities.Completed},
		},
	}, owner.Id.Hex()))
	if err == nil {
		t.Fatal("expected invalid bulk transition")
	}
	for _, id := range []string{first.Id, second.Id} {
		objectID, _ := primitive.ObjectIDFromHex(id)
		var stored entities.Chore
		if err := f.DB.Collection("Chore").FindOne(f.Ctx, bson.M{"_id": objectID}).Decode(&stored); err != nil {
			t.Fatal(err)
		}
		if stored.Status != entities.Draft || stored.Version != 0 {
			t.Fatalf("bulk update was partially committed: %+v", stored)
		}
	}

	errs := make([]error, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range errs {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, errs[i] = cqrs.Send[[]dtos.ChoreResponseModel](f.Ctx, f.Sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
				HouseId: house.Id.Hex(), Chores: []dtos.UpdateChoreStatusModel{{ChoreId: first.Id, Status: entities.Progress}},
			}, owner.Id.Hex()))
		}(i)
	}
	close(start)
	workers.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful transitions = %d, want 1", successes)
	}
	historyCount, err := f.DB.Collection("ChoreStatusHistory").CountDocuments(f.Ctx, bson.M{"choreId": first.Id})
	if err != nil {
		t.Fatal(err)
	}
	if historyCount != 2 {
		t.Fatalf("history count = %d, want 2", historyCount)
	}
}

func TestRequiredChoreReviewPhotoUploadsAndStoresRoundKey(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	house := f.CreateHouse(t, owner, 1)
	if _, err := f.DB.Collection("House").UpdateByID(f.Ctx, house.Id, bson.M{
		"$set": bson.M{"isChoreReviewPhotoRequired": true},
	}); err != nil {
		t.Fatal(err)
	}

	chore, err := cqrs.Send[*dtos.ChoreResponseModel](f.Ctx, f.Sender, appkit.CreateChoreCommand(dtos.CreateChoreModel{
		Title: "Photo chore", Description: "Review photo integration test", AssignedTo: owner.Id.Hex(),
		DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Easy,
	}, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[[]dtos.ChoreResponseModel](f.Ctx, f.Sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
		HouseId: house.Id.Hex(),
		Chores:  []dtos.UpdateChoreStatusModel{{ChoreId: chore.Id, Status: entities.Progress}},
	}, owner.Id.Hex())); err != nil {
		t.Fatal(err)
	}

	withoutPhoto := updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
		HouseId: house.Id.Hex(),
		Chores:  []dtos.UpdateChoreStatusModel{{ChoreId: chore.Id, Status: entities.InTest}},
	}, owner.Id.Hex())
	if _, err := cqrs.Send[[]dtos.ChoreResponseModel](f.Ctx, f.Sender, withoutPhoto); !helpers.IsApplicationError(err, "chore.error.review_photo_required") {
		t.Fatalf("missing photo error=%v", err)
	}

	photo := []byte{0xff, 0xd8, 0xff, 0xe0, 0x01, 0x02}
	withPhoto := withoutPhoto
	withPhoto.Photo = &choreCommands.ChoreReviewPhoto{Reader: bytes.NewReader(photo), Size: int64(len(photo))}
	result, err := cqrs.Send[[]dtos.ChoreResponseModel](f.Ctx, f.Sender, withPhoto)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || len(result[0].ReviewPhotoRounds) != 1 || result[0].ReviewPhotoRounds[0] != 1 {
		t.Fatalf("review photo rounds=%v", result)
	}

	choreID, _ := primitive.ObjectIDFromHex(chore.Id)
	var stored entities.Chore
	if err := f.DB.Collection("Chore").FindOne(f.Ctx, bson.M{"_id": choreID}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	objectKey := stored.ReviewPhotoKeys["1"]
	wantPrefix := "houses/" + house.Id.Hex() + "/chores/" + chore.Id + "/review-rounds/1/"
	if !strings.HasPrefix(objectKey, wantPrefix) || !strings.HasSuffix(objectKey, ".jpg") {
		t.Fatalf("object key=%q, want round-scoped JPEG key", objectKey)
	}
	if uploaded := f.PhotoStore.Object(objectKey); !bytes.Equal(uploaded, photo) {
		t.Fatalf("uploaded photo=%x, want %x", uploaded, photo)
	}

	photoURL, err := cqrs.Send[*dtos.ChoreReviewPhotoURLModel](f.Ctx, f.Sender, choreQueries.GetReviewPhotoURLQuery{
		ChoreID: chore.Id, Round: 1, RequesterID: owner.Id.Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if photoURL.URL != "https://storage.test/"+objectKey || photoURL.ExpiresAt.IsZero() {
		t.Fatalf("unexpected presigned response: %+v", photoURL)
	}
	f.PhotoStore.Mu.Lock()
	presignedKey, presignedTTL := f.PhotoStore.PresignedKey, f.PhotoStore.PresignedTTL
	f.PhotoStore.Mu.Unlock()
	if presignedKey != objectKey || presignedTTL != 5*time.Minute {
		t.Fatalf("presigned key=%q ttl=%s", presignedKey, presignedTTL)
	}

	outsider := f.SeedUser(t, "correct-password")
	_, err = cqrs.Send[*dtos.ChoreReviewPhotoURLModel](f.Ctx, f.Sender, choreQueries.GetReviewPhotoURLQuery{
		ChoreID: chore.Id, Round: 1, RequesterID: outsider.Id.Hex(),
	})
	if !helpers.IsApplicationError(err, "house.error.user_not_member") {
		t.Fatalf("outsider access error=%v", err)
	}
}

func TestConcurrentReviewVotesCompleteExactlyOnce(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	house := f.CreateHouse(t, owner, 3)
	inviteCode := f.GenerateHouseInviteCode(t, house, owner)
	reviewers := []entities.User{f.SeedUser(t, "correct-password"), f.SeedUser(t, "correct-password")}
	for _, reviewer := range reviewers {
		if _, err := cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.JoinHouseCommand{UserID: reviewer.Id.Hex(), InviteCode: inviteCode}); err != nil {
			t.Fatal(err)
		}
	}
	chore, err := cqrs.Send[*dtos.ChoreResponseModel](f.Ctx, f.Sender, appkit.CreateChoreCommand(dtos.CreateChoreModel{
		Title: "Review chore", Description: "Concurrent review test", AssignedTo: owner.Id.Hex(),
		DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Medium,
	}, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []entities.ChoreStatus{entities.Progress, entities.InTest} {
		if _, err := cqrs.Send[[]dtos.ChoreResponseModel](f.Ctx, f.Sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
			HouseId: house.Id.Hex(), Chores: []dtos.UpdateChoreStatusModel{{ChoreId: chore.Id, Status: status}},
		}, owner.Id.Hex())); err != nil {
			t.Fatal(err)
		}
	}

	errs := make([]error, len(reviewers))
	approved := true
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range reviewers {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, errs[i] = cqrs.Send[*dtos.ChoreResponseModel](f.Ctx, f.Sender, choreCommands.ReviewChoreCommand{
				ChoreID: chore.Id, ReviewerID: reviewers[i].Id.Hex(), IsApproved: approved,
			})
		}(i)
	}
	close(start)
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent review failed: %v", err)
		}
	}
	id, _ := primitive.ObjectIDFromHex(chore.Id)
	var stored entities.Chore
	if err := f.DB.Collection("Chore").FindOne(f.Ctx, bson.M{"_id": id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != entities.Completed || !stored.IsCompleted {
		t.Fatalf("chore not completed: %+v", stored)
	}
	votes, _ := f.DB.Collection("ChoreReviewVote").CountDocuments(f.Ctx, bson.M{"choreId": chore.Id, "reviewRound": 1})
	completedHistory, _ := f.DB.Collection("ChoreStatusHistory").CountDocuments(f.Ctx, bson.M{"choreId": chore.Id, "status": entities.Completed})
	if votes != 2 || completedHistory != 1 {
		t.Fatalf("votes=%d completed histories=%d, want 2 and 1", votes, completedHistory)
	}
}

func TestConcurrentFailedLoginsDoNotLoseIncrements(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	user := f.SeedUser(t, "correct-password")
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, _ = cqrs.Send[string](f.Ctx, f.Sender, authCommands.LoginCommand{
				Email: user.Email, Password: "wrong-password",
			})
		}()
	}
	close(start)
	workers.Wait()
	var stored entities.User
	if err := f.DB.Collection("User").FindOne(f.Ctx, bson.M{"_id": user.Id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.FailedLoginAttempts != 10 || stored.IsActive {
		t.Fatalf("failed attempts=%d active=%v, want 10 and false", stored.FailedLoginAttempts, stored.IsActive)
	}
}

func TestConcurrentAnnouncementAndProfileLimits(t *testing.T) {
	f := appkit.NewConcurrencyFixture(t)
	owner := f.SeedUser(t, "correct-password")
	house := f.CreateHouse(t, owner, 2)
	announcementErrors := make([]error, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range announcementErrors {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, announcementErrors[i] = cqrs.Send[*dtos.AnnouncementResponseModel](f.Ctx, f.Sender, housecommands.CreateAnnouncementCommand{
				HouseID: house.Id.Hex(), UserID: owner.Id.Hex(), Title: fmt.Sprintf("Notice %d", i), Description: "Concurrent announcement",
			})
		}(i)
	}
	close(start)
	workers.Wait()
	successes := 0
	for _, err := range announcementErrors {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful announcements = %d, want 1", successes)
	}

	firstName, secondName := "First", "Second"
	profileErrors := make([]error, 2)
	start = make(chan struct{})
	for i, name := range []*string{&firstName, &secondName} {
		workers.Add(1)
		go func(i int, name *string) {
			defer workers.Done()
			<-start
			_, profileErrors[i] = cqrs.Send[*dtos.UserResultModel](f.Ctx, f.Sender, userCommands.UpdateProfileCommand{
				UserID: owner.Id.Hex(), Firstname: name,
			})
		}(i, name)
	}
	close(start)
	workers.Wait()
	successes = 0
	for _, err := range profileErrors {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful profile updates = %d, want 1; errors=%v", successes, profileErrors)
	}
	historyCount, err := f.DB.Collection("UserInfoHistory").CountDocuments(f.Ctx, bson.M{
		"userId": owner.Id.Hex(), "columnName": entities.UserInfoColumnFirstName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("profile history count = %d, want 1", historyCount)
	}
}

func TestTransactionsRollbackWhenSecondWriteFails(t *testing.T) {
	t.Run("house and user membership", func(t *testing.T) {
		f := appkit.NewConcurrencyFixture(t)
		owner := f.SeedUser(t, "correct-password")
		if err := f.DB.RunCommand(f.Ctx, bson.D{
			{Key: "collMod", Value: "User"},
			{Key: "validator", Value: bson.M{"$jsonSchema": bson.M{
				"bsonType": "object", "properties": bson.M{"houseIds": bson.M{"bsonType": "array", "maxItems": 0}},
			}}},
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.CreateHouseCommand{
			OwnerID: owner.Id.Hex(), Name: "Must roll back", Type: entities.SharedHouse, MaxMemberCount: 2,
		}); err == nil {
			t.Fatal("expected user validation failure")
		}
		houseCount, err := f.DB.Collection("House").CountDocuments(f.Ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		if houseCount != 0 {
			t.Fatalf("house insert was not rolled back: count=%d", houseCount)
		}
	})

	t.Run("join house and user membership", func(t *testing.T) {
		f := appkit.NewConcurrencyFixture(t)
		owner := f.SeedUser(t, "correct-password")
		house := f.CreateHouse(t, owner, 2)
		member := f.SeedUser(t, "correct-password")
		inviteCode := f.GenerateHouseInviteCode(t, house, owner)
		if err := f.DB.RunCommand(f.Ctx, bson.D{
			{Key: "collMod", Value: "User"},
			{Key: "validator", Value: bson.M{"$jsonSchema": bson.M{
				"bsonType": "object", "properties": bson.M{"houseIds": bson.M{"bsonType": "array", "maxItems": 0}},
			}}},
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.JoinHouseCommand{
			UserID: member.Id.Hex(), InviteCode: inviteCode,
		}); err == nil {
			t.Fatal("expected user validation failure")
		}
		var storedHouse entities.House
		if err := f.DB.Collection("House").FindOne(f.Ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
			t.Fatal(err)
		}
		if len(storedHouse.MemberIds) != 1 || storedHouse.MemberIds[0] != owner.Id.Hex() {
			t.Fatalf("house membership was not rolled back: %v", storedHouse.MemberIds)
		}
	})

	t.Run("chore and initial history", func(t *testing.T) {
		f := appkit.NewConcurrencyFixture(t)
		owner := f.SeedUser(t, "correct-password")
		house := f.CreateHouse(t, owner, 2)
		if err := f.DB.RunCommand(f.Ctx, bson.D{
			{Key: "collMod", Value: "ChoreStatusHistory"},
			{Key: "validator", Value: bson.M{"$jsonSchema": bson.M{
				"bsonType": "object", "required": bson.A{"neverAllowed"},
			}}},
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cqrs.Send[*dtos.ChoreResponseModel](f.Ctx, f.Sender, appkit.CreateChoreCommand(dtos.CreateChoreModel{
			Title: "Must roll back", Description: "History insert must fail", AssignedTo: owner.Id.Hex(),
			DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Easy,
		}, owner.Id.Hex())); err == nil {
			t.Fatal("expected history validation failure")
		}
		choreCount, err := f.DB.Collection("Chore").CountDocuments(f.Ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		if choreCount != 0 {
			t.Fatalf("chore insert was not rolled back: count=%d", choreCount)
		}
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
