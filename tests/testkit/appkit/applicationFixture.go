// Package appkit holds the application fixture (houses, users, chores and
// their command handlers on a pooled database) for the account, house and
// chore test packages.
package appkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	authCommands "houseflowApi/internal/application/auth/commands"
	authPolicies "houseflowApi/internal/application/auth/policies"
	authQueries "houseflowApi/internal/application/auth/queries"
	choreCommands "houseflowApi/internal/application/chore/commands"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	choreQueries "houseflowApi/internal/application/chore/queries"
	houseApplication "houseflowApi/internal/application/house"
	housecommands "houseflowApi/internal/application/house/commands"
	housePolicies "houseflowApi/internal/application/house/policies"
	housequeries "houseflowApi/internal/application/house/queries"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	localizationCommands "houseflowApi/internal/application/localization/commands"
	localizationQueries "houseflowApi/internal/application/localization/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	infrastructureLocalization "houseflowApi/internal/infrastructure/localization"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// seedHouseData writes one document of every house-scoped kind. Validators are
// bypassed: only the house and chore links matter here.
func (f *ConcurrencyFixture) SeedHouseData(t *testing.T, houseID string) {
	t.Helper()
	choreID := primitive.NewObjectID()
	insert := func(collection string, document bson.M) {
		if _, err := f.DB.Collection(collection).InsertOne(f.Ctx, document,
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

func (f *ConcurrencyFixture) HouseDataCount(t *testing.T, houseID primitive.ObjectID) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	count := func(collection string, filter bson.M) {
		value, err := f.DB.Collection(collection).CountDocuments(f.Ctx, filter)
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

type ConcurrencyFixture struct {
	Ctx         context.Context
	DB          *mongo.Database
	Sender      cqrs.Sender
	EmailSender *RecordingEmailSender
	PhotoStore  *RecordingObjectStorage
}

type RecordingObjectStorage struct {
	Mu           sync.Mutex
	objects      map[string][]byte
	PresignedKey string
	PresignedTTL time.Duration
}

func newRecordingObjectStorage() *RecordingObjectStorage {
	return &RecordingObjectStorage{objects: make(map[string][]byte)}
}

func (s *RecordingObjectStorage) PutImage(_ context.Context, objectKey string, reader io.Reader, size int64, _ string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("uploaded size=%d, want %d", len(data), size)
	}
	s.Mu.Lock()
	s.objects[objectKey] = data
	s.Mu.Unlock()
	return nil
}

func (s *RecordingObjectStorage) DeleteObject(_ context.Context, objectKey string) error {
	s.Mu.Lock()
	delete(s.objects, objectKey)
	s.Mu.Unlock()
	return nil
}

func (s *RecordingObjectStorage) PresignGet(_ context.Context, objectKey string, ttl time.Duration) (string, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if _, exists := s.objects[objectKey]; !exists {
		return "", fmt.Errorf("object %q does not exist", objectKey)
	}
	s.PresignedKey = objectKey
	s.PresignedTTL = ttl
	return "https://storage.test/" + objectKey, nil
}

func (s *RecordingObjectStorage) Object(objectKey string) []byte {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return append([]byte(nil), s.objects[objectKey]...)
}

type RecordingEmailSender struct {
	ResetCode        string
	VerificationCode string
}

func (s *RecordingEmailSender) SendResetCodeEmail(_ string, code string, _ int) error {
	s.ResetCode = code
	return nil
}

func (s *RecordingEmailSender) SendEmailVerificationCode(_ string, code string, _ int) error {
	s.VerificationCode = code
	return nil
}

func NewConcurrencyFixture(t *testing.T) *ConcurrencyFixture {
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
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test client: %v", err)
		}
	})
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatal(err)
	}
	// A migrated database from the package's pool, reset after the test.
	db := testkit.AcquireMigratedDatabase(t, client)

	users := database.NewDbContext[entities.User](client, db.Name())
	houses := database.NewDbContext[entities.House](client, db.Name())
	houseInviteCodes := database.NewDbContext[entities.HouseInviteCode](client, db.Name())
	houseInfoHistories := database.NewDbContext[entities.HouseInfoHistory](client, db.Name())
	imageAssets := database.NewDbContext[entities.ImageAsset](client, db.Name())
	localizations := database.NewDbContext[entities.Localization](client, db.Name())
	languages := database.NewDbContext[entities.LocalizationLanguageOption](client, db.Name())
	chores := database.NewDbContext[entities.Chore](client, db.Name())
	histories := database.NewDbContext[entities.UserInfoHistory](client, db.Name())
	announcements := database.NewDbContext[entities.Announcement](client, db.Name())
	choreStatusHistories := database.NewDbContext[entities.ChoreStatusHistory](client, db.Name())
	choreReviewVotes := database.NewDbContext[entities.ChoreReviewVote](client, db.Name())
	membershipPolicy := housePolicies.NewMembershipPolicy(houses)
	houseInfoReader := houseApplication.NewInfoReader(users)
	createHouseHandler := housecommands.NewCreateHouseHandler(houses, users)
	const joinCodeSecret = "test-join-code-secret"
	generateHouseInviteCodeHandler := housecommands.NewGenerateHouseInviteCodeHandler(houses, houseInviteCodes, joinCodeSecret, 2)
	joinHouseHandler := housecommands.NewJoinHouseHandler(houses, users, houseInviteCodes, joinCodeSecret)
	createAnnouncementHandler := housecommands.NewCreateAnnouncementHandler(membershipPolicy, users, announcements)
	updateHouseProfileHandler := housecommands.NewUpdateHouseProfileHandler(
		membershipPolicy, houses, houseInfoHistories, houseInfoReader,
	)
	exitHouseHandler := housecommands.NewExitHouseHandler(houses, users, houseInviteCodes)
	getHouseInfosHandler := housequeries.NewGetHouseInfosHandler(membershipPolicy, houseInfoReader)
	getHouseDetailsHandler := housequeries.NewGetHouseDetailsHandler(
		membershipPolicy, houses, users, chores, choreStatusHistories, choreReviewVotes, announcements,
	)
	assignmentPolicy := chorePolicies.NewAssignmentPolicy(users)
	workflowPolicy := chorePolicies.NewWorkflowPolicy()
	photoStore := newRecordingObjectStorage()
	createChoreHandler := choreCommands.NewCreateChoreHandler(
		chores, choreStatusHistories, membershipPolicy, assignmentPolicy,
	)
	updateChoreHandler := choreCommands.NewUpdateChoreHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, assignmentPolicy, workflowPolicy,
	)
	updateChoreStatusHandler := choreCommands.NewUpdateChoreStatusHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, workflowPolicy, photoStore,
	)
	reviewChoreHandler := choreCommands.NewReviewChoreHandler(
		chores, choreStatusHistories, choreReviewVotes, membershipPolicy, workflowPolicy,
	)
	getReviewPhotoURLHandler := choreQueries.NewGetReviewPhotoURLHandler(chores, membershipPolicy, photoStore)
	updateProfileHandler := userCommands.NewUpdateProfileHandler(users, histories)
	createUserHandler := userCommands.NewCreateUserHandler(users, histories)
	userIdentities := database.NewDbContext[entities.UserIdentity](client, db.Name())
	deleteUserHandler := userCommands.NewDeleteUserHandler(users, houses, userIdentities, nil)
	getUserByEmailHandler := userQueries.NewGetUserByEmailHandler(users)
	listUsersHandler := userQueries.NewListUsersHandler(users)
	getUsersByHouseHandler := userQueries.NewGetUsersByHouseHandler(users, membershipPolicy)
	imageCache := helpers.NewInMemoryCache[[]dtos.ImageAssetResultModel]()
	createImageAssetHandler := imageAssetCommands.NewCreateImageAssetHandler(imageAssets, imageCache)
	updateImageAssetHandler := imageAssetCommands.NewUpdateImageAssetHandler(imageAssets, imageCache)
	getImagesByCategoryHandler := imageAssetQueries.NewGetImagesByCategoryHandler(imageAssets, imageCache)
	getImageByPublicIDHandler := imageAssetQueries.NewGetImageByPublicIDHandler(imageAssets)
	localizationCache := infrastructureLocalization.NewCache(localizations)
	getPlaintextsHandler := localizationQueries.NewGetPlaintextsHandler(localizations, localizationCache)
	getLanguagesHandler := localizationQueries.NewGetLanguagesHandler(languages)
	getLanguageHandler := localizationQueries.NewGetLanguageHandler(languages)
	insertLocalizationsHandler := localizationCommands.NewInsertLocalizationsHandler(localizations, localizationCache)
	insertLocalizationLanguageHandler := localizationCommands.NewInsertLocalizationLanguageHandler(languages)
	emailSender := &RecordingEmailSender{}
	jwtService := helpers.NewJWTService("test-jwt-secret")
	const resetSecret = "test-reset-secret"
	const validityMinutes = 5
	loginHandler := authCommands.NewLoginHandler(users, jwtService)
	signUpHandler := authCommands.NewSignUpHandler(users, histories, jwtService)
	forgotPasswordHandler := authCommands.NewForgotPasswordHandler(users, emailSender, resetSecret, validityMinutes)
	codeAttempts := authPolicies.NewCodeAttempts(database.NewDbContext[entities.AuthCodeAttempt](client, db.Name()))
	resetPasswordHandler := authCommands.NewResetPasswordHandler(users, codeAttempts, resetSecret, validityMinutes)
	sendEmailVerificationCodeHandler := authCommands.NewSendEmailVerificationCodeHandler(users, emailSender, resetSecret, validityMinutes)
	validateEmailHandler := authCommands.NewValidateEmailHandler(users, codeAttempts, resetSecret, validityMinutes)
	validateAuthHandler := authQueries.NewValidateAuthHandler(users, houses, userIdentities, jwtService)
	sender := cqrs.New()
	cqrs.MustRegister[string, authCommands.LoginCommand](sender, loginHandler)
	cqrs.MustRegister[string, authCommands.SignUpCommand](sender, signUpHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ForgotPasswordCommand](sender, forgotPasswordHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ResetPasswordCommand](sender, resetPasswordHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.SendEmailVerificationCodeCommand](sender, sendEmailVerificationCodeHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ValidateEmailCommand](sender, validateEmailHandler)
	cqrs.MustRegister[*dtos.AuthUserResultModel, authQueries.ValidateAuthQuery](sender, validateAuthHandler)
	cqrs.MustRegister[[]dtos.LocalizationPlaintextResponseModel, localizationQueries.GetPlaintextsQuery](sender, getPlaintextsHandler)
	cqrs.MustRegister[[]dtos.LocalizationLanguageResponseModel, localizationQueries.GetLanguagesQuery](sender, getLanguagesHandler)
	cqrs.MustRegister[[]dtos.LocalizationLanguageResponseModel, localizationQueries.GetLanguageQuery](sender, getLanguageHandler)
	cqrs.MustRegister[cqrs.NoResult, localizationCommands.InsertLocalizationsCommand](sender, insertLocalizationsHandler)
	cqrs.MustRegister[cqrs.NoResult, localizationCommands.InsertLocalizationLanguageCommand](sender, insertLocalizationLanguageHandler)
	cqrs.MustRegister[*entities.House, housecommands.CreateHouseCommand](sender, createHouseHandler)
	cqrs.MustRegister[*dtos.HouseInviteCodeResponseModel, housecommands.GenerateHouseInviteCodeCommand](sender, generateHouseInviteCodeHandler)
	cqrs.MustRegister[*entities.House, housecommands.JoinHouseCommand](sender, joinHouseHandler)
	cqrs.MustRegister[*dtos.AnnouncementResponseModel, housecommands.CreateAnnouncementCommand](sender, createAnnouncementHandler)
	cqrs.MustRegister[*dtos.HouseInfosResponseModel, housecommands.UpdateHouseProfileCommand](sender, updateHouseProfileHandler)
	cqrs.MustRegister[cqrs.NoResult, housecommands.ExitHouseCommand](sender, exitHouseHandler)
	cqrs.MustRegister[*dtos.HouseInfosResponseModel, housequeries.GetHouseInfosQuery](sender, getHouseInfosHandler)
	cqrs.MustRegister[*dtos.HouseDetailsModel, housequeries.GetHouseDetailsQuery](sender, getHouseDetailsHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.CreateChoreCommand](sender, createChoreHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.UpdateChoreCommand](sender, updateChoreHandler)
	cqrs.MustRegister[[]dtos.ChoreResponseModel, choreCommands.UpdateChoreStatusCommand](sender, updateChoreStatusHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.ReviewChoreCommand](sender, reviewChoreHandler)
	cqrs.MustRegister[*dtos.ChoreReviewPhotoURLModel, choreQueries.GetReviewPhotoURLQuery](sender, getReviewPhotoURLHandler)
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
	return &ConcurrencyFixture{
		Ctx: ctx, DB: db, Sender: sender, EmailSender: emailSender, PhotoStore: photoStore,
	}
}

func CreateChoreCommand(model dtos.CreateChoreModel, requesterID string) choreCommands.CreateChoreCommand {
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

func (f *ConcurrencyFixture) SeedUser(t *testing.T, password string) entities.User {
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
	if _, err := f.DB.Collection("User").InsertOne(f.Ctx, user); err != nil {
		t.Fatal(err)
	}
	return user
}

func (f *ConcurrencyFixture) CreateHouse(t *testing.T, owner entities.User, capacity int) *entities.House {
	t.Helper()
	house, err := cqrs.Send[*entities.House](f.Ctx, f.Sender, housecommands.CreateHouseCommand{
		OwnerID: owner.Id.Hex(), Name: "Concurrent house", Type: entities.SharedHouse, MaxMemberCount: capacity,
	})
	if err != nil {
		t.Fatal(err)
	}
	return house
}

func (f *ConcurrencyFixture) GenerateHouseInviteCode(t *testing.T, house *entities.House, owner entities.User) string {
	t.Helper()
	response, err := cqrs.Send[*dtos.HouseInviteCodeResponseModel](f.Ctx, f.Sender, housecommands.GenerateHouseInviteCodeCommand{
		HouseID: house.Id.Hex(), UserID: owner.Id.Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return response.InviteCode
}
