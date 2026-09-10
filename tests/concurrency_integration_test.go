package tests

import (
	"context"
	"fmt"
	"houseflowApi/external/migration"
	"houseflowApi/internal/abstract"
	choreCommands "houseflowApi/internal/application/chore/commands"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	housecommands "houseflowApi/internal/application/house/commands"
	housePolicies "houseflowApi/internal/application/house/policies"
	housequeries "houseflowApi/internal/application/house/queries"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

type concurrencyFixture struct {
	ctx    context.Context
	db     *mongo.Database
	sender cqrs.Sender
	auth   *services.AuthService
}

func newConcurrencyFixture(t *testing.T) *concurrencyFixture {
	t.Helper()
	uri := os.Getenv("HOUSEFLOW_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("HOUSEFLOW_TEST_MONGO_URI is required for concurrency integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetServerSelectionTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("houseflow_test_" + primitive.NewObjectID().Hex())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test client: %v", err)
		}
	})
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatal(err)
	}
	if err := migration.RunAll(ctx, db, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}

	users := abstract.New[entities.User](client, db.Name())
	houses := abstract.New[entities.House](client, db.Name())
	imageAssets := abstract.New[entities.ImageAsset](client, db.Name())
	chores := abstract.New[entities.Chore](client, db.Name())
	histories := abstract.New[entities.UserInfoHistory](client, db.Name())
	announcements := abstract.New[entities.Announcement](client, db.Name())
	choreStatusHistories := abstract.New[entities.ChoreStatusHistory](client, db.Name())
	choreReviewVotes := abstract.New[entities.ChoreReviewVote](client, db.Name())
	membershipPolicy := housePolicies.NewMembershipPolicy(houses)
	createHouseHandler := housecommands.NewCreateHouseHandler(houses, users)
	joinHouseHandler := housecommands.NewJoinHouseHandler(houses, users)
	createAnnouncementHandler := housecommands.NewCreateAnnouncementHandler(membershipPolicy, users, announcements)
	getHouseDetailsHandler := housequeries.NewGetHouseDetailsHandler(
		membershipPolicy, houses, users, chores, choreStatusHistories, choreReviewVotes, announcements,
	)
	assignmentPolicy := chorePolicies.NewAssignmentPolicy(users)
	workflowPolicy := chorePolicies.NewWorkflowPolicy()
	createChoreHandler := choreCommands.NewCreateChoreHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, assignmentPolicy,
	)
	updateChoreHandler := choreCommands.NewUpdateChoreHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, assignmentPolicy,
	)
	updateChoreStatusHandler := choreCommands.NewUpdateChoreStatusHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, workflowPolicy,
	)
	reviewChoreHandler := choreCommands.NewReviewChoreHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, workflowPolicy,
	)
	updateProfileHandler := userCommands.NewUpdateProfileHandler(users, histories)
	createUserHandler := userCommands.NewCreateUserHandler(users, histories)
	deleteUserHandler := userCommands.NewDeleteUserHandler(users)
	getUserByEmailHandler := userQueries.NewGetUserByEmailHandler(users)
	listUsersHandler := userQueries.NewListUsersHandler(users)
	getUsersByHouseHandler := userQueries.NewGetUsersByHouseHandler(users, membershipPolicy)
	imageCache := helpers.NewInMemoryCache[[]dtos.ImageAssetResultModel]()
	createImageAssetHandler := imageAssetCommands.NewCreateImageAssetHandler(imageAssets, imageCache)
	updateImageAssetHandler := imageAssetCommands.NewUpdateImageAssetHandler(imageAssets, imageCache)
	getImagesByCategoryHandler := imageAssetQueries.NewGetImagesByCategoryHandler(imageAssets, imageCache)
	getImageByPublicIDHandler := imageAssetQueries.NewGetImageByPublicIDHandler(imageAssets)
	sender := cqrs.New()
	cqrs.MustRegister[*entities.House, housecommands.CreateHouseCommand](sender, createHouseHandler)
	cqrs.MustRegister[*entities.House, housecommands.JoinHouseCommand](sender, joinHouseHandler)
	cqrs.MustRegister[*dtos.AnnouncementResponseModel, housecommands.CreateAnnouncementCommand](sender, createAnnouncementHandler)
	cqrs.MustRegister[*dtos.HouseDetailsModel, housequeries.GetHouseDetailsQuery](sender, getHouseDetailsHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.CreateChoreCommand](sender, createChoreHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.UpdateChoreCommand](sender, updateChoreHandler)
	cqrs.MustRegister[[]dtos.ChoreResponseModel, choreCommands.UpdateChoreStatusCommand](sender, updateChoreStatusHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.ReviewChoreCommand](sender, reviewChoreHandler)
	cqrs.MustRegister[*dtos.UserResultModel, userCommands.UpdateProfileCommand](sender, updateProfileHandler)
	cqrs.MustRegister[*dtos.NewUserModel, userCommands.CreateUserCommand](sender, createUserHandler)
	cqrs.MustRegister[cqrs.NoResult, userCommands.DeleteUserCommand](sender, deleteUserHandler)
	cqrs.MustRegister[*dtos.UserResultModel, userQueries.GetUserByEmailQuery](sender, getUserByEmailHandler)
	cqrs.MustRegister[[]dtos.UserResultModel, userQueries.ListUsersQuery](sender, listUsersHandler)
	cqrs.MustRegister[[]dtos.UserResultModel, userQueries.GetUsersByHouseQuery](sender, getUsersByHouseHandler)
	cqrs.MustRegister[cqrs.NoResult, imageAssetCommands.CreateImageAssetCommand](sender, createImageAssetHandler)
	cqrs.MustRegister[cqrs.NoResult, imageAssetCommands.UpdateImageAssetCommand](sender, updateImageAssetHandler)
	cqrs.MustRegister[[]dtos.ImageAssetResultModel, imageAssetQueries.GetImagesByCategoryQuery](sender, getImagesByCategoryHandler)
	cqrs.MustRegister[*dtos.ImageAssetResultModel, imageAssetQueries.GetImageByPublicIDQuery](sender, getImageByPublicIDHandler)
	return &concurrencyFixture{
		ctx: ctx, db: db,
		sender: sender,
		auth:   services.NewAuthService(users, nil, histories),
	}
}

func createChoreCommand(model dtos.CreateChoreModel, requesterID string) choreCommands.CreateChoreCommand {
	return choreCommands.CreateChoreCommand{
		Title:             model.Title,
		Description:       model.Description,
		AssignedTo:        model.AssignedTo,
		DueDate:           model.DueDate.Time,
		HouseID:           model.HouseId,
		Level:             model.Level,
		IsRecurring:       model.IsRecurring,
		RecurringInterval: model.RecurringInterval,
		RequesterID:       requesterID,
	}
}

func updateChoreStatusCommand(model dtos.BulkUpdateChoreStatusModel, userID string) choreCommands.UpdateChoreStatusCommand {
	updates := make([]choreCommands.ChoreStatusUpdate, 0, len(model.Chores))
	for _, update := range model.Chores {
		updates = append(updates, choreCommands.ChoreStatusUpdate{ChoreID: update.ChoreId, Status: update.Status})
	}
	return choreCommands.UpdateChoreStatusCommand{HouseID: model.HouseId, Chores: updates, UserID: userID}
}

func TestGetHouseDetailsQueryReturnsCompleteSnapshot(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 2)
	member := f.seedUser(t, "correct-password")
	if _, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.JoinHouseCommand{
		UserID: member.Id.Hex(), InviteCode: house.InviteCode,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[*dtos.AnnouncementResponseModel](f.ctx, f.sender, housecommands.CreateAnnouncementCommand{
		HouseID: house.Id.Hex(), UserID: owner.Id.Hex(), Title: "Snapshot notice", Description: "Snapshot test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[*dtos.ChoreResponseModel](f.ctx, f.sender, createChoreCommand(dtos.CreateChoreModel{
		Title:       "Snapshot chore",
		Description: "Snapshot test chore",
		AssignedTo:  owner.Id.Hex(),
		DueDate:     dtos.NewUTCDateTime(time.Now().Add(time.Hour)),
		HouseId:     house.Id.Hex(),
		Level:       entities.Easy,
	}, owner.Id.Hex())); err != nil {
		t.Fatal(err)
	}

	details, err := cqrs.Send[*dtos.HouseDetailsModel](f.ctx, f.sender, housequeries.GetHouseDetailsQuery{
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

func (f *concurrencyFixture) seedUser(t *testing.T, password string) entities.User {
	t.Helper()
	hash, err := helpers.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	id := primitive.NewObjectID()
	now := time.Now()
	user := entities.User{
		Id: id, Firstname: "Test", Lastname: "User", Email: id.Hex() + "@example.test",
		HashPassword: hash, Language: "en", HouseIds: []string{}, IsActive: true,
		CreatedOn: now, UpdatedOn: now, LastLogin: now,
	}
	if _, err := f.db.Collection("User").InsertOne(f.ctx, user); err != nil {
		t.Fatal(err)
	}
	return user
}

func (f *concurrencyFixture) createHouse(t *testing.T, owner entities.User, capacity int) *entities.House {
	t.Helper()
	house, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.CreateHouseCommand{
		OwnerID: owner.Id.Hex(), Name: "Concurrent house", Type: entities.SharedHouse, MaxMemberCount: capacity,
	})
	if err != nil {
		t.Fatal(err)
	}
	return house
}

func TestConcurrentHouseJoinsRespectCapacityAndKeepMembershipConsistent(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 5)
	users := make([]entities.User, 16)
	for i := range users {
		users[i] = f.seedUser(t, "correct-password")
	}

	errs := make([]error, len(users))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range users {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, errs[i] = cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.JoinHouseCommand{
				UserID: users[i].Id.Hex(), InviteCode: house.InviteCode,
			})
		}(i)
	}
	close(start)
	workers.Wait()

	var storedHouse entities.House
	if err := f.db.Collection("House").FindOne(f.ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
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
		if err := f.db.Collection("User").FindOne(f.ctx, bson.M{"_id": users[i].Id}).Decode(&storedUser); err != nil {
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
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	errs := make([]error, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range errs {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, errs[i] = cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.CreateHouseCommand{
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
	if err := f.db.Collection("User").FindOne(f.ctx, bson.M{"_id": owner.Id}).Decode(&storedUser); err != nil {
		t.Fatal(err)
	}
	houses, err := f.db.Collection("House").CountDocuments(f.ctx, bson.M{"ownerId": owner.Id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if houses != 8 || len(storedUser.HouseIds) != 8 {
		t.Fatalf("houses=%d user.houseIds=%d, want 8 and 8", houses, len(storedUser.HouseIds))
	}
}

func TestChoreBulkRollbackAndConcurrentTransition(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 2)
	member := f.seedUser(t, "correct-password")
	if _, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.JoinHouseCommand{UserID: member.Id.Hex(), InviteCode: house.InviteCode}); err != nil {
		t.Fatal(err)
	}
	create := func(title string) *dtos.ChoreResponseModel {
		result, err := cqrs.Send[*dtos.ChoreResponseModel](f.ctx, f.sender, createChoreCommand(dtos.CreateChoreModel{
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
	_, err := cqrs.Send[[]dtos.ChoreResponseModel](f.ctx, f.sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
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
		if err := f.db.Collection("Chore").FindOne(f.ctx, bson.M{"_id": objectID}).Decode(&stored); err != nil {
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
			_, errs[i] = cqrs.Send[[]dtos.ChoreResponseModel](f.ctx, f.sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
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
	historyCount, err := f.db.Collection("ChoreStatusHistory").CountDocuments(f.ctx, bson.M{"choreId": first.Id})
	if err != nil {
		t.Fatal(err)
	}
	if historyCount != 2 {
		t.Fatalf("history count = %d, want 2", historyCount)
	}
}

func TestConcurrentReviewVotesCompleteExactlyOnce(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 3)
	reviewers := []entities.User{f.seedUser(t, "correct-password"), f.seedUser(t, "correct-password")}
	for _, reviewer := range reviewers {
		if _, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.JoinHouseCommand{UserID: reviewer.Id.Hex(), InviteCode: house.InviteCode}); err != nil {
			t.Fatal(err)
		}
	}
	chore, err := cqrs.Send[*dtos.ChoreResponseModel](f.ctx, f.sender, createChoreCommand(dtos.CreateChoreModel{
		Title: "Review chore", Description: "Concurrent review test", AssignedTo: owner.Id.Hex(),
		DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Medium,
	}, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []entities.ChoreStatus{entities.Progress, entities.InTest} {
		if _, err := cqrs.Send[[]dtos.ChoreResponseModel](f.ctx, f.sender, updateChoreStatusCommand(dtos.BulkUpdateChoreStatusModel{
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
			_, errs[i] = cqrs.Send[*dtos.ChoreResponseModel](f.ctx, f.sender, choreCommands.ReviewChoreCommand{
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
	if err := f.db.Collection("Chore").FindOne(f.ctx, bson.M{"_id": id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != entities.Completed || !stored.IsCompleted {
		t.Fatalf("chore not completed: %+v", stored)
	}
	votes, _ := f.db.Collection("ChoreReviewVote").CountDocuments(f.ctx, bson.M{"choreId": chore.Id, "reviewRound": 1})
	completedHistory, _ := f.db.Collection("ChoreStatusHistory").CountDocuments(f.ctx, bson.M{"choreId": chore.Id, "status": entities.Completed})
	if votes != 2 || completedHistory != 1 {
		t.Fatalf("votes=%d completed histories=%d, want 2 and 1", votes, completedHistory)
	}
}

func TestConcurrentFailedLoginsDoNotLoseIncrements(t *testing.T) {
	f := newConcurrencyFixture(t)
	user := f.seedUser(t, "correct-password")
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, _ = f.auth.Login(f.ctx, user.Email, "wrong-password")
		}()
	}
	close(start)
	workers.Wait()
	var stored entities.User
	if err := f.db.Collection("User").FindOne(f.ctx, bson.M{"_id": user.Id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.FailedLoginAttempts != 10 || stored.IsActive {
		t.Fatalf("failed attempts=%d active=%v, want 10 and false", stored.FailedLoginAttempts, stored.IsActive)
	}
}

func TestConcurrentAnnouncementAndProfileLimits(t *testing.T) {
	f := newConcurrencyFixture(t)
	owner := f.seedUser(t, "correct-password")
	house := f.createHouse(t, owner, 2)
	announcementErrors := make([]error, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range announcementErrors {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, announcementErrors[i] = cqrs.Send[*dtos.AnnouncementResponseModel](f.ctx, f.sender, housecommands.CreateAnnouncementCommand{
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
			_, profileErrors[i] = cqrs.Send[*dtos.UserResultModel](f.ctx, f.sender, userCommands.UpdateProfileCommand{
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
	historyCount, err := f.db.Collection("UserInfoHistory").CountDocuments(f.ctx, bson.M{
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
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		if err := f.db.RunCommand(f.ctx, bson.D{
			{Key: "collMod", Value: "User"},
			{Key: "validator", Value: bson.M{"$jsonSchema": bson.M{
				"bsonType": "object", "properties": bson.M{"houseIds": bson.M{"bsonType": "array", "maxItems": 0}},
			}}},
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.CreateHouseCommand{
			OwnerID: owner.Id.Hex(), Name: "Must roll back", Type: entities.SharedHouse, MaxMemberCount: 2,
		}); err == nil {
			t.Fatal("expected user validation failure")
		}
		houseCount, err := f.db.Collection("House").CountDocuments(f.ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		if houseCount != 0 {
			t.Fatalf("house insert was not rolled back: count=%d", houseCount)
		}
	})

	t.Run("join house and user membership", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 2)
		member := f.seedUser(t, "correct-password")
		if err := f.db.RunCommand(f.ctx, bson.D{
			{Key: "collMod", Value: "User"},
			{Key: "validator", Value: bson.M{"$jsonSchema": bson.M{
				"bsonType": "object", "properties": bson.M{"houseIds": bson.M{"bsonType": "array", "maxItems": 0}},
			}}},
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cqrs.Send[*entities.House](f.ctx, f.sender, housecommands.JoinHouseCommand{
			UserID: member.Id.Hex(), InviteCode: house.InviteCode,
		}); err == nil {
			t.Fatal("expected user validation failure")
		}
		var storedHouse entities.House
		if err := f.db.Collection("House").FindOne(f.ctx, bson.M{"_id": house.Id}).Decode(&storedHouse); err != nil {
			t.Fatal(err)
		}
		if len(storedHouse.MemberIds) != 1 || storedHouse.MemberIds[0] != owner.Id.Hex() {
			t.Fatalf("house membership was not rolled back: %v", storedHouse.MemberIds)
		}
	})

	t.Run("chore and initial history", func(t *testing.T) {
		f := newConcurrencyFixture(t)
		owner := f.seedUser(t, "correct-password")
		house := f.createHouse(t, owner, 2)
		if err := f.db.RunCommand(f.ctx, bson.D{
			{Key: "collMod", Value: "ChoreStatusHistory"},
			{Key: "validator", Value: bson.M{"$jsonSchema": bson.M{
				"bsonType": "object", "required": bson.A{"neverAllowed"},
			}}},
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cqrs.Send[*dtos.ChoreResponseModel](f.ctx, f.sender, createChoreCommand(dtos.CreateChoreModel{
			Title: "Must roll back", Description: "History insert must fail", AssignedTo: owner.Id.Hex(),
			DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Easy,
		}, owner.Id.Hex())); err == nil {
			t.Fatal("expected history validation failure")
		}
		choreCount, err := f.db.Collection("Chore").CountDocuments(f.ctx, bson.M{})
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
