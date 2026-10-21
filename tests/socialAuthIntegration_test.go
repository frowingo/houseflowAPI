package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	authCommands "houseflowApi/internal/application/auth/commands"
	authPolicies "houseflowApi/internal/application/auth/policies"
	authQueries "houseflowApi/internal/application/auth/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

type fakeSocialVerifier struct {
	provider entities.AuthProvider
	identity authAbstract.VerifiedIdentity
	err      error
}

func (v *fakeSocialVerifier) Provider() entities.AuthProvider { return v.provider }

func (v *fakeSocialVerifier) Verify(context.Context, authAbstract.SocialCredential) (authAbstract.VerifiedIdentity, error) {
	return v.identity, v.err
}

type keptAuthorization struct {
	token    string
	clientID string
}

type fakeAuthorizationKeeper struct {
	mutex   sync.Mutex
	kept    int
	revoked []keptAuthorization
}

func (k *fakeAuthorizationKeeper) Provider() entities.AuthProvider { return entities.ProviderApple }

func (k *fakeAuthorizationKeeper) Keep(_ context.Context, identity authAbstract.VerifiedIdentity, credential authAbstract.SocialCredential) (string, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	k.kept++
	return "kept:" + identity.ProviderUserID + ":" + credential.AuthorizationCode, nil
}

func (k *fakeAuthorizationKeeper) Revoke(_ context.Context, keptToken string, clientID string) error {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	k.revoked = append(k.revoked, keptAuthorization{token: keptToken, clientID: clientID})
	return nil
}

type socialAuthFixture struct {
	*concurrencyFixture
	apple         *fakeSocialVerifier
	snapchat      *fakeSocialVerifier
	keeper        *fakeAuthorizationKeeper
	mail          *recordingEmailSender
	pendingTokens *helpers.SocialPendingTokenService
	handler       *authCommands.SocialAuthHandler
	sendCode      *authCommands.SendSocialSignUpCodeHandler
	complete      *authCommands.CompleteSocialSignUpHandler
	deleteUser    *userCommands.DeleteUserHandler
	validate      *authQueries.ValidateAuthHandler
}

func newSocialAuthFixture(t *testing.T) *socialAuthFixture {
	t.Helper()
	base := newConcurrencyFixture(t)
	client, dbName := base.db.Client(), base.db.Name()
	users := database.NewDbContext[entities.User](client, dbName)
	houses := database.NewDbContext[entities.House](client, dbName)
	identities := database.NewDbContext[entities.UserIdentity](client, dbName)
	histories := database.NewDbContext[entities.UserInfoHistory](client, dbName)
	jwtService := helpers.NewJWTService("social-auth-jwt-secret")

	apple := &fakeSocialVerifier{provider: entities.ProviderApple, identity: authAbstract.VerifiedIdentity{
		Provider: entities.ProviderApple, ProviderUserID: "apple-sub-1", ClientID: "com.houseflow.app",
		Email: "Relay@PrivateRelay.AppleId.com", EmailVerified: true, IsPrivateRelay: true,
	}}
	snapchat := &fakeSocialVerifier{provider: entities.ProviderSnapchat, identity: authAbstract.VerifiedIdentity{
		Provider: entities.ProviderSnapchat, ProviderUserID: "snap-external-1", ClientID: "snap-client", FirstName: "Snappy",
	}}
	keeper := &fakeAuthorizationKeeper{}
	mail := &recordingEmailSender{}
	accounts := authPolicies.NewSocialAccounts(users, identities, histories, jwtService)
	pendingTokens := helpers.NewSocialPendingTokenService("social-auth-jwt-secret")
	return &socialAuthFixture{
		concurrencyFixture: base,
		apple:              apple,
		snapchat:           snapchat,
		keeper:             keeper,
		mail:               mail,
		pendingTokens:      pendingTokens,
		handler: authCommands.NewSocialAuthHandler(accounts, pendingTokens,
			[]authAbstract.SocialIdentityVerifier{apple, snapchat}, []authAbstract.SocialAuthorizationKeeper{keeper}),
		sendCode:   authCommands.NewSendSocialSignUpCodeHandler(accounts, pendingTokens, mail, "social-code-secret", 5),
		complete:   authCommands.NewCompleteSocialSignUpHandler(accounts, pendingTokens, "social-code-secret", 5),
		deleteUser: userCommands.NewDeleteUserHandler(users, houses, identities, []authAbstract.SocialAuthorizationKeeper{keeper}),
		validate:   authQueries.NewValidateAuthHandler(users, houses, identities, jwtService),
	}
}

func (f *socialAuthFixture) appleSignIn(t *testing.T) (dtos.SocialAuthResponseModel, error) {
	t.Helper()
	return f.handler.Handle(f.ctx, authCommands.SocialAuthCommand{
		Provider:   entities.ProviderApple,
		Credential: authAbstract.SocialCredential{IDToken: "token", Nonce: "nonce", AuthorizationCode: "code-1"},
		Firstname:  "  Grace ", Lastname: "Hopper", Language: "tr",
	})
}

func (f *socialAuthFixture) count(t *testing.T, collection string, filter bson.M) int64 {
	t.Helper()
	count, err := f.db.Collection(collection).CountDocuments(f.ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestSocialSignUpCreatesAccountAndSignsInAgain(t *testing.T) {
	fixture := newSocialAuthFixture(t)

	created, err := fixture.appleSignIn(t)
	if err != nil {
		t.Fatal(err)
	}
	if !created.IsNewUser || created.Token == "" {
		t.Fatalf("first sign-in=%+v, want a new user with a token", created)
	}

	var user entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"email": "relay@privaterelay.appleid.com"}).Decode(&user); err != nil {
		t.Fatalf("user not stored with normalized email: %v", err)
	}
	if user.SignUpProvider != entities.ProviderApple || user.HashPassword != "" || !user.IsVerifyEmail ||
		user.Firstname != "Grace" || user.Lastname != "Hopper" || user.Language != "tr" || !user.IsActive {
		t.Fatalf("stored user=%+v", user)
	}
	var identity entities.UserIdentity
	if err := fixture.db.Collection("UserIdentity").FindOne(fixture.ctx, bson.M{"userId": user.Id.Hex()}).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity.Provider != entities.ProviderApple || identity.ProviderUserId != "apple-sub-1" || identity.ProviderClientId != "com.houseflow.app" ||
		identity.AppleRefreshTokenEnc != "kept:apple-sub-1:code-1" || !identity.IsPrivateRelay || !identity.EmailVerified {
		t.Fatalf("stored identity=%+v", identity)
	}
	if history := fixture.count(t, "UserInfoHistory", bson.M{"userId": user.Id.Hex()}); history != 3 {
		t.Fatalf("history entries=%d, want 3", history)
	}

	authenticated, err := fixture.validate.Handle(fixture.ctx, authQueries.ValidateAuthQuery{Token: created.Token})
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.SignUpProvider != entities.ProviderApple || authenticated.HasPassword ||
		len(authenticated.LinkedProviders) != 1 || authenticated.LinkedProviders[0] != entities.ProviderApple {
		t.Fatalf("isAuth providers=%d/%v hasPassword=%v", authenticated.SignUpProvider, authenticated.LinkedProviders, authenticated.HasPassword)
	}

	signedIn, err := fixture.appleSignIn(t)
	if err != nil {
		t.Fatal(err)
	}
	if signedIn.IsNewUser || signedIn.Token == "" {
		t.Fatalf("second sign-in=%+v, want existing user", signedIn)
	}
	if fixture.keeper.kept != 1 {
		t.Fatalf("authorization kept %d times, want only at sign-up", fixture.keeper.kept)
	}
	if users := fixture.count(t, "User", bson.M{}); users != 1 {
		t.Fatalf("users=%d, want 1", users)
	}
}

func TestSocialSignUpDoesNotMergeExistingEmailAccount(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	hashedPassword, err := helpers.HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := fixture.db.Collection("User").InsertOne(fixture.ctx, entities.User{
		Firstname: "Existing", Lastname: "User", Email: "RELAY@privaterelay.appleid.COM", HashPassword: hashedPassword,
		Language: "en", HouseIds: []string{}, IsActive: true, CreatedOn: now, UpdatedOn: now, LastLogin: now,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = fixture.appleSignIn(t)
	if !helpers.IsApplicationError(err, "auth.error.email_registered_with_other_method") {
		t.Fatalf("err=%v, want auth.error.email_registered_with_other_method", err)
	}
	if identities := fixture.count(t, "UserIdentity", bson.M{}); identities != 0 {
		t.Fatalf("identities=%d, want none", identities)
	}
	if fixture.keeper.kept != 0 {
		t.Fatal("authorization kept for a rejected sign-up")
	}
}

func TestSocialAuthRejections(t *testing.T) {
	fixture := newSocialAuthFixture(t)

	_, err := fixture.handler.Handle(fixture.ctx, authCommands.SocialAuthCommand{Provider: entities.ProviderGoogle})
	if !helpers.IsApplicationError(err, "auth.error.provider_not_supported") {
		t.Fatalf("disabled provider err=%v", err)
	}

	fixture.apple.err = helpers.NewUnauthorizedError("auth.error.invalid_social_token")
	if _, err := fixture.appleSignIn(t); !helpers.IsApplicationError(err, "auth.error.invalid_social_token") {
		t.Fatalf("verifier error err=%v", err)
	}
	fixture.apple.err = nil

	fixture.apple.identity.Email = ""
	if _, err := fixture.appleSignIn(t); !helpers.IsApplicationError(err, "auth.error.social_email_required") {
		t.Fatalf("missing email err=%v", err)
	}
	if users := fixture.count(t, "User", bson.M{}); users != 0 {
		t.Fatalf("users=%d after rejected sign-ups, want 0", users)
	}
}

func TestSocialSignInRejectsLockedAccount(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	if _, err := fixture.appleSignIn(t); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection("User").UpdateMany(fixture.ctx, bson.M{}, bson.M{"$set": bson.M{"isActive": false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.appleSignIn(t); !helpers.IsApplicationError(err, "auth.error.account_locked") {
		t.Fatalf("err=%v, want auth.error.account_locked", err)
	}
}

func TestConcurrentSocialSignUpsCreateOneAccount(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	const requests = 6
	var wait sync.WaitGroup
	results := make([]dtos.SocialAuthResponseModel, requests)
	errs := make([]error, requests)
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = fixture.appleSignIn(t)
		}(index)
	}
	wait.Wait()

	newUsers := 0
	for index := range results {
		if errs[index] != nil {
			t.Fatalf("request %d: %v", index, errs[index])
		}
		if results[index].IsNewUser {
			newUsers++
		}
	}
	if newUsers != 1 {
		t.Fatalf("requests reporting a new user=%d, want 1", newUsers)
	}
	if users, identities := fixture.count(t, "User", bson.M{}), fixture.count(t, "UserIdentity", bson.M{}); users != 1 || identities != 1 {
		t.Fatalf("users=%d identities=%d, want 1 and 1", users, identities)
	}
}

func TestSocialSignInRecoversFromOrphanedIdentity(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	if _, err := fixture.appleSignIn(t); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection("User").DeleteMany(fixture.ctx, bson.M{}); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.appleSignIn(t)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsNewUser {
		t.Fatal("orphaned identity did not lead to a new account")
	}
	if users, identities := fixture.count(t, "User", bson.M{}), fixture.count(t, "UserIdentity", bson.M{}); users != 1 || identities != 1 {
		t.Fatalf("users=%d identities=%d, want 1 and 1", users, identities)
	}
}

func TestDeleteUserRemovesIdentitiesAndRevokesApple(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	if _, err := fixture.appleSignIn(t); err != nil {
		t.Fatal(err)
	}
	var user entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{}).Decode(&user); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.deleteUser.Handle(fixture.ctx, userCommands.DeleteUserCommand{UserID: user.Id.Hex()}); err != nil {
		t.Fatal(err)
	}
	if identities := fixture.count(t, "UserIdentity", bson.M{}); identities != 0 {
		t.Fatalf("identities=%d after delete, want 0", identities)
	}
	want := []keptAuthorization{{token: "kept:apple-sub-1:code-1", clientID: "com.houseflow.app"}}
	if len(fixture.keeper.revoked) != 1 || fixture.keeper.revoked[0] != want[0] {
		t.Fatalf("revoked=%+v, want %+v", fixture.keeper.revoked, want)
	}
}
