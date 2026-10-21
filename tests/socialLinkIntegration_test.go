package tests

import (
	"context"
	"sync"
	"testing"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	authCommands "houseflowApi/internal/application/auth/commands"
	userPolicies "houseflowApi/internal/application/user/policies"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
)

type fakeNotificationVerifier struct {
	event authAbstract.ProviderAccountEvent
}

func (v *fakeNotificationVerifier) Provider() entities.AuthProvider { return entities.ProviderApple }

func (v *fakeNotificationVerifier) VerifyNotification(context.Context, string) (authAbstract.ProviderAccountEvent, error) {
	return v.event, nil
}

type socialLinkFixture struct {
	*socialAuthFixture
	google        *fakeSocialVerifier
	link          *authCommands.LinkSocialProviderHandler
	unlink        *authCommands.UnlinkSocialProviderHandler
	notifications *fakeNotificationVerifier
	accountEvents *authCommands.ProviderAccountEventHandler
}

func newSocialLinkFixture(t *testing.T) *socialLinkFixture {
	t.Helper()
	social := newSocialAuthFixture(t)
	client, dbName := social.db.Client(), social.db.Name()
	users := database.NewDbContext[entities.User](client, dbName)
	houses := database.NewDbContext[entities.House](client, dbName)
	identities := database.NewDbContext[entities.UserIdentity](client, dbName)

	google := &fakeSocialVerifier{provider: entities.ProviderGoogle, identity: authAbstract.VerifiedIdentity{
		Provider: entities.ProviderGoogle, ProviderUserID: "google-sub-1", ClientID: "google-client",
		Email: "linked@gmail.com", EmailVerified: true,
	}}
	keepers := []authAbstract.SocialAuthorizationKeeper{social.keeper}
	notifications := &fakeNotificationVerifier{}
	return &socialLinkFixture{
		socialAuthFixture: social,
		google:            google,
		link: authCommands.NewLinkSocialProviderHandler(users, identities,
			[]authAbstract.SocialIdentityVerifier{social.apple, google}, keepers),
		unlink:        authCommands.NewUnlinkSocialProviderHandler(users, identities, keepers),
		notifications: notifications,
		accountEvents: authCommands.NewProviderAccountEventHandler(identities,
			userPolicies.NewAccountRemoval(users, houses, identities, keepers),
			[]authAbstract.ProviderNotificationVerifier{notifications}),
	}
}

func (f *socialLinkFixture) linkProvider(user entities.User, provider entities.AuthProvider) ([]entities.AuthProvider, error) {
	result, err := f.link.Handle(f.ctx, authCommands.LinkSocialProviderCommand{
		UserID: user.Id.Hex(), Provider: provider,
		Credential: authAbstract.SocialCredential{IDToken: "token", Nonce: "nonce", AuthorizationCode: "link-code"},
	})
	return result.LinkedProviders, err
}

func (f *socialLinkFixture) unlinkProvider(user entities.User, provider entities.AuthProvider) ([]entities.AuthProvider, error) {
	result, err := f.unlink.Handle(f.ctx, authCommands.UnlinkSocialProviderCommand{UserID: user.Id.Hex(), Provider: provider})
	return result.LinkedProviders, err
}

// seedSocialOnlyUser creates a user without a password, linked to Apple only.
func (f *socialLinkFixture) seedSocialOnlyUser(t *testing.T) entities.User {
	t.Helper()
	if _, err := f.appleSignIn(t); err != nil {
		t.Fatal(err)
	}
	var user entities.User
	if err := f.db.Collection("User").FindOne(f.ctx, bson.M{"signUpProvider": entities.ProviderApple}).Decode(&user); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestLinkProviderLetsPasswordUserSignInWithIt(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	user := fixture.seedUser(t, "correct-password")

	linked, err := fixture.linkProvider(user, entities.ProviderApple)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 1 || linked[0] != entities.ProviderApple {
		t.Fatalf("linked=%v, want [apple]", linked)
	}
	var identity entities.UserIdentity
	if err := fixture.db.Collection("UserIdentity").FindOne(fixture.ctx, bson.M{"userId": user.Id.Hex()}).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity.ProviderUserId != "apple-sub-1" || identity.AppleRefreshTokenEnc != "kept:apple-sub-1:link-code" {
		t.Fatalf("identity=%+v", identity)
	}

	signedIn, err := fixture.appleSignIn(t)
	if err != nil {
		t.Fatal(err)
	}
	if signedIn.IsNewUser || signedIn.Token == "" {
		t.Fatalf("sign-in after link=%+v, want the existing account", signedIn)
	}
	if users := fixture.count(t, "User", bson.M{}); users != 1 {
		t.Fatalf("users=%d, want 1", users)
	}

	// Linking the same account again changes nothing.
	again, err := fixture.linkProvider(user, entities.ProviderApple)
	if err != nil || len(again) != 1 || fixture.keeper.kept != 1 {
		t.Fatalf("relink=%v err=%v kept=%d", again, err, fixture.keeper.kept)
	}
}

func TestLinkProviderRejectsConflicts(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	owner := fixture.seedUser(t, "correct-password")
	other := fixture.seedUser(t, "correct-password")
	if _, err := fixture.linkProvider(owner, entities.ProviderApple); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.linkProvider(other, entities.ProviderApple); !helpers.IsApplicationError(err, "auth.error.provider_already_linked") {
		t.Fatalf("taking another user's provider account err=%v", err)
	}

	fixture.apple.identity.ProviderUserID = "apple-sub-2"
	if _, err := fixture.linkProvider(owner, entities.ProviderApple); !helpers.IsApplicationError(err, "auth.error.provider_already_connected") {
		t.Fatalf("second account of the same provider err=%v", err)
	}

	if _, err := fixture.link.Handle(fixture.ctx, authCommands.LinkSocialProviderCommand{
		UserID: owner.Id.Hex(), Provider: entities.ProviderFacebook,
	}); !helpers.IsApplicationError(err, "auth.error.provider_not_supported") {
		t.Fatalf("disabled provider err=%v", err)
	}

	if _, err := fixture.db.Collection("User").UpdateOne(fixture.ctx, bson.M{"_id": other.Id}, bson.M{"$set": bson.M{"isActive": false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.linkProvider(other, entities.ProviderGoogle); !helpers.IsApplicationError(err, "auth.error.account_locked") {
		t.Fatalf("locked account err=%v", err)
	}
	if identities := fixture.count(t, "UserIdentity", bson.M{}); identities != 1 {
		t.Fatalf("identities=%d, want 1", identities)
	}
}

func TestUnlinkProviderKeepsTheLastSignInMethod(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	user := fixture.seedSocialOnlyUser(t)

	if _, err := fixture.unlinkProvider(user, entities.ProviderApple); !helpers.IsApplicationError(err, "auth.error.last_sign_in_method") {
		t.Fatalf("removing the only sign-in method err=%v", err)
	}
	if _, err := fixture.unlinkProvider(user, entities.ProviderGoogle); !helpers.IsApplicationError(err, "auth.error.provider_not_linked") {
		t.Fatalf("removing a provider that is not linked err=%v", err)
	}

	if _, err := fixture.linkProvider(user, entities.ProviderGoogle); err != nil {
		t.Fatal(err)
	}
	linked, err := fixture.unlinkProvider(user, entities.ProviderApple)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 1 || linked[0] != entities.ProviderGoogle {
		t.Fatalf("linked=%v, want [google]", linked)
	}
	if len(fixture.keeper.revoked) != 1 || fixture.keeper.revoked[0].token != "kept:apple-sub-1:code-1" {
		t.Fatalf("revoked=%+v, want the Apple authorization", fixture.keeper.revoked)
	}
}

func TestUnlinkProviderWithPassword(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	user := fixture.seedUser(t, "correct-password")
	if _, err := fixture.linkProvider(user, entities.ProviderGoogle); err != nil {
		t.Fatal(err)
	}
	linked, err := fixture.unlinkProvider(user, entities.ProviderGoogle)
	if err != nil || len(linked) != 0 {
		t.Fatalf("linked=%v err=%v, want none", linked, err)
	}
}

func TestConcurrentUnlinksLeaveOneSignInMethod(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	user := fixture.seedSocialOnlyUser(t)
	if _, err := fixture.linkProvider(user, entities.ProviderGoogle); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errs := make([]error, 2)
	for index, provider := range []entities.AuthProvider{entities.ProviderApple, entities.ProviderGoogle} {
		wait.Add(1)
		go func(index int, provider entities.AuthProvider) {
			defer wait.Done()
			_, errs[index] = fixture.unlinkProvider(user, provider)
		}(index, provider)
	}
	wait.Wait()

	if identities := fixture.count(t, "UserIdentity", bson.M{"userId": user.Id.Hex()}); identities != 1 {
		t.Fatalf("identities=%d after concurrent unlinks (errs %v), want 1", identities, errs)
	}
	failures := 0
	for _, err := range errs {
		if err != nil {
			if !helpers.IsApplicationError(err, "auth.error.last_sign_in_method") {
				t.Fatalf("unexpected error %v", err)
			}
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("failures=%d, want exactly one rejected unlink", failures)
	}
}

func (f *socialLinkFixture) appleEvent(t *testing.T, eventType authAbstract.ProviderAccountEventType, subject string) {
	t.Helper()
	f.notifications.event = authAbstract.ProviderAccountEvent{Type: eventType, ProviderUserID: subject}
	if _, err := f.accountEvents.Handle(f.ctx, authCommands.ProviderAccountEventCommand{
		Provider: entities.ProviderApple, Payload: "payload",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAppleConsentRevokedRemovesOnlyTheLink(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	user := fixture.seedUser(t, "correct-password")
	if _, err := fixture.linkProvider(user, entities.ProviderApple); err != nil {
		t.Fatal(err)
	}

	fixture.appleEvent(t, authAbstract.ProviderEmailChanged, "apple-sub-1")
	if identities := fixture.count(t, "UserIdentity", bson.M{}); identities != 1 {
		t.Fatalf("email event changed identities: %d", identities)
	}

	fixture.appleEvent(t, authAbstract.ProviderConsentRevoked, "apple-sub-1")
	fixture.appleEvent(t, authAbstract.ProviderConsentRevoked, "apple-sub-1")
	if identities := fixture.count(t, "UserIdentity", bson.M{}); identities != 0 {
		t.Fatalf("identities=%d after consent revoked, want 0", identities)
	}
	if users := fixture.count(t, "User", bson.M{"_id": user.Id}); users != 1 {
		t.Fatal("account deleted on consent revoked")
	}
}

func TestAppleAccountDeletedDeletesTheAccount(t *testing.T) {
	fixture := newSocialLinkFixture(t)
	user := fixture.seedUser(t, "correct-password")
	house := fixture.createHouse(t, user, 1)
	if _, err := fixture.linkProvider(user, entities.ProviderApple); err != nil {
		t.Fatal(err)
	}
	survivor := fixture.seedUser(t, "correct-password")

	fixture.appleEvent(t, authAbstract.ProviderAccountDeleted, "apple-sub-1")
	// Apple has been seen to repeat this event; the repeat is a no-op.
	fixture.appleEvent(t, authAbstract.ProviderAccountDeleted, "apple-sub-1")
	fixture.appleEvent(t, authAbstract.ProviderAccountDeleted, "unknown-sub")

	for collection, filter := range map[string]bson.M{
		"User": {"_id": user.Id}, "UserIdentity": {}, "House": {"_id": house.Id},
	} {
		if count := fixture.count(t, collection, filter); count != 0 {
			t.Fatalf("%s left=%d, want deleted", collection, count)
		}
	}
	if count := fixture.count(t, "User", bson.M{"_id": survivor.Id}); count != 1 {
		t.Fatal("unrelated user deleted")
	}
}
