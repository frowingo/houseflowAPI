package tests

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
	userPolicies "houseflowApi/internal/application/user/policies"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

type facebookDeletionFixture struct {
	*concurrencyFixture
	handler *authCommands.ProviderDataDeletionHandler
	app     *fiber.App
}

func newFacebookDeletionFixture(t *testing.T, enabled bool) *facebookDeletionFixture {
	t.Helper()
	base := newConcurrencyFixture(t)
	client, dbName := base.db.Client(), base.db.Name()
	users := database.NewDbContext[entities.User](client, dbName)
	houses := database.NewDbContext[entities.House](client, dbName)
	identities := database.NewDbContext[entities.UserIdentity](client, dbName)
	deletions := database.NewDbContext[entities.DataDeletionRequest](client, dbName)

	var verifiers []authAbstract.DataDeletionRequestVerifier
	if enabled {
		clock := &testClock{now: time.Now()}
		verifiers = append(verifiers, newTestFacebookVerifier(t, newFakeGraphServer(t), newTestJWKSServer(t, newRSATestKey(t, "fb-1")), clock))
	}
	handler := authCommands.NewProviderDataDeletionHandler(identities, deletions,
		userPolicies.NewAccountRemoval(users, houses, identities, nil), verifiers)

	mediator := cqrs.New()
	cqrs.MustRegister[dtos.ProviderDataDeletionResult, authCommands.ProviderDataDeletionCommand](mediator, handler)
	cqrs.MustRegister[dtos.DataDeletionStatusResponseModel, authQueries.GetDataDeletionStatusQuery](mediator,
		authQueries.NewGetDataDeletionStatusHandler(deletions))
	authController := controllers.NewAuthController(mediator)
	app := fiber.New()
	app.Post("/api/v1/auth/facebook/data-deletion", authController.FacebookDataDeletion)
	app.Get("/api/v1/auth/facebook/data-deletion/:code", authController.DataDeletionStatus)

	return &facebookDeletionFixture{concurrencyFixture: base, handler: handler, app: app}
}

func (f *facebookDeletionFixture) linkFacebook(t *testing.T, user entities.User, facebookUserID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := f.db.Collection("UserIdentity").InsertOne(f.ctx, entities.UserIdentity{
		UserId: user.Id.Hex(), Provider: entities.ProviderFacebook, ProviderUserId: facebookUserID,
		ProviderClientId: testFacebookAppID, CreatedOn: now, LastLoginOn: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *facebookDeletionFixture) postDeletion(t *testing.T, signedRequest string) (int, dtos.FacebookDataDeletionResponseModel) {
	t.Helper()
	request := httptest.NewRequest(fiber.MethodPost, "http://api.houseflow.test/api/v1/auth/facebook/data-deletion",
		strings.NewReader(url.Values{"signed_request": {signedRequest}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Fly terminates TLS and forwards the original scheme.
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := f.app.Test(request, int((10 * time.Second).Milliseconds()))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body dtos.FacebookDataDeletionResponseModel
	if response.StatusCode == fiber.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, body
}

func deletionRequestFor(t *testing.T, facebookUserID string) string {
	return facebookSignedRequest(t, testFacebookAppSecret, map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": facebookUserID, "issued_at": time.Now().Unix(),
	}, false)
}

func TestFacebookDataDeletionAlwaysDeletesTheAccount(t *testing.T) {
	fixture := newFacebookDeletionFixture(t, true)
	// The user also has a password: the account is deleted anyway.
	user := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, user, 1)
	fixture.linkFacebook(t, user, "fb-user-1")
	survivor := fixture.seedUser(t, "correct-password")

	status, body := fixture.postDeletion(t, deletionRequestFor(t, "fb-user-1"))
	if status != fiber.StatusOK || len(body.ConfirmationCode) != 32 {
		t.Fatalf("status=%d body=%+v", status, body)
	}
	if want := "https://api.houseflow.test/api/v1/auth/facebook/data-deletion/" + body.ConfirmationCode; body.URL != want {
		t.Fatalf("status url=%q, want %q", body.URL, want)
	}

	for collection, filter := range map[string]bson.M{
		"User":         {"_id": user.Id},
		"UserIdentity": {"userId": user.Id.Hex()},
		"House":        {"_id": house.Id},
	} {
		if count, err := fixture.db.Collection(collection).CountDocuments(fixture.ctx, filter); err != nil || count != 0 {
			t.Fatalf("%s left=%d (err %v), want deleted", collection, count, err)
		}
	}
	if count, err := fixture.db.Collection("User").CountDocuments(fixture.ctx, bson.M{"_id": survivor.Id}); err != nil || count != 1 {
		t.Fatalf("unrelated user count=%d (err %v)", count, err)
	}

	var record entities.DataDeletionRequest
	if err := fixture.db.Collection("DataDeletionRequest").FindOne(fixture.ctx, bson.M{"_id": body.ConfirmationCode}).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if !record.AccountDeleted || record.Status != entities.DataDeletionCompleted || record.Provider != entities.ProviderFacebook ||
		record.ExpiresAt.Before(time.Now().Add(80*24*time.Hour)) {
		t.Fatalf("record=%+v", record)
	}

	response, err := fixture.app.Test(httptest.NewRequest(fiber.MethodGet, "/api/v1/auth/facebook/data-deletion/"+body.ConfirmationCode, nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	var statusBody dtos.DataDeletionStatusResponseModel
	if err := json.NewDecoder(response.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusOK || statusBody.Status != "completed" || statusBody.ConfirmationCode != body.ConfirmationCode {
		t.Fatalf("status endpoint=%d %+v", response.StatusCode, statusBody)
	}

	// Facebook may send the same request again; it still succeeds.
	if status, _ := fixture.postDeletion(t, deletionRequestFor(t, "fb-user-1")); status != fiber.StatusOK {
		t.Fatalf("repeated request status=%d", status)
	}
}

func TestFacebookDataDeletionForUnknownUserStillConfirms(t *testing.T) {
	fixture := newFacebookDeletionFixture(t, true)
	user := fixture.seedUser(t, "correct-password")

	status, body := fixture.postDeletion(t, deletionRequestFor(t, "fb-unknown"))
	if status != fiber.StatusOK || body.ConfirmationCode == "" {
		t.Fatalf("status=%d body=%+v", status, body)
	}
	var record entities.DataDeletionRequest
	if err := fixture.db.Collection("DataDeletionRequest").FindOne(fixture.ctx, bson.M{"_id": body.ConfirmationCode}).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.AccountDeleted {
		t.Fatal("record claims an account was deleted")
	}
	if count, err := fixture.db.Collection("User").CountDocuments(fixture.ctx, bson.M{"_id": user.Id}); err != nil || count != 1 {
		t.Fatalf("user count=%d (err %v), want untouched", count, err)
	}
}

func TestFacebookDataDeletionRejectsForgedRequests(t *testing.T) {
	fixture := newFacebookDeletionFixture(t, true)
	user := fixture.seedUser(t, "correct-password")
	fixture.linkFacebook(t, user, "fb-user-1")

	forged := facebookSignedRequest(t, "attacker-secret", map[string]any{"algorithm": "HMAC-SHA256", "user_id": "fb-user-1"}, false)
	if status, _ := fixture.postDeletion(t, forged); status != fiber.StatusUnauthorized {
		t.Fatalf("forged request status=%d, want 401", status)
	}
	if status, _ := fixture.postDeletion(t, ""); status != fiber.StatusBadRequest {
		t.Fatalf("empty request status=%d, want 400", status)
	}
	if count, err := fixture.db.Collection("User").CountDocuments(fixture.ctx, bson.M{"_id": user.Id}); err != nil || count != 1 {
		t.Fatalf("user count=%d (err %v), want untouched", count, err)
	}
	if count, err := fixture.db.Collection("DataDeletionRequest").CountDocuments(fixture.ctx, bson.M{}); err != nil || count != 0 {
		t.Fatalf("records=%d (err %v), want none", count, err)
	}

	response, err := fixture.app.Test(httptest.NewRequest(fiber.MethodGet, "/api/v1/auth/facebook/data-deletion/"+strings.Repeat("a", 32), nil), -1)
	if err != nil || response.StatusCode != fiber.StatusNotFound {
		t.Fatalf("unknown code status=%v err=%v, want 404", response.StatusCode, err)
	}
}

func TestFacebookDataDeletionWhenFacebookIsDisabled(t *testing.T) {
	fixture := newFacebookDeletionFixture(t, false)
	_, err := fixture.handler.Handle(fixture.ctx, authCommands.ProviderDataDeletionCommand{
		Provider: entities.ProviderFacebook, SignedRequest: deletionRequestFor(t, "fb-user-1"),
	})
	if !helpers.IsApplicationError(err, "auth.error.provider_not_supported") {
		t.Fatalf("err=%v, want auth.error.provider_not_supported", err)
	}
}
