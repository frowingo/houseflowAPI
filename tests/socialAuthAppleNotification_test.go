package tests

import (
	"context"
	"testing"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/infrastructure/socialAuth"

	"github.com/golang-jwt/jwt/v5"
)

func appleNotificationClaims(now time.Time, events any) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://appleid.apple.com", "aud": testAppleClientID, "jti": "notification-1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "events": events,
	}
}

func TestAppleNotificationEvents(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "apple-1")
	verifier := socialAuth.NewAppleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{testAppleClientID}, socialAuth.WithIDTokenClock(clock.Now))

	testCases := map[string]struct {
		events any
		want   authAbstract.ProviderAccountEventType
	}{
		"consent revoked":        {`{"type":"consent-revoked","sub":"apple-sub-1","event_time":1}`, authAbstract.ProviderConsentRevoked},
		"account delete":         {`{"type":"account-delete","sub":"apple-sub-1","event_time":1}`, authAbstract.ProviderAccountDeleted},
		"account deleted":        {`{"type":"account-deleted","sub":"apple-sub-1","event_time":1}`, authAbstract.ProviderAccountDeleted},
		"email disabled":         {`{"type":"email-disabled","sub":"apple-sub-1","email":"x@privaterelay.appleid.com"}`, authAbstract.ProviderEmailChanged},
		"unknown type":           {`{"type":"something-new","sub":"apple-sub-1"}`, authAbstract.ProviderEventUnknown},
		"events as a JSON value": {map[string]any{"type": "consent-revoked", "sub": "apple-sub-1"}, authAbstract.ProviderConsentRevoked},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			event, err := verifier.VerifyNotification(context.Background(), key.sign(t, appleNotificationClaims(clock.Now(), testCase.events)))
			if err != nil {
				t.Fatal(err)
			}
			if event.Type != testCase.want || event.ProviderUserID != "apple-sub-1" {
				t.Fatalf("event=%+v, want type %s for apple-sub-1", event, testCase.want)
			}
		})
	}
}

func TestAppleNotificationRejectsInvalidPayloads(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "apple-1")
	verifier := socialAuth.NewAppleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{testAppleClientID}, socialAuth.WithIDTokenClock(clock.Now))
	valid := `{"type":"consent-revoked","sub":"apple-sub-1"}`

	otherApp := appleNotificationClaims(clock.Now(), valid)
	otherApp["aud"] = "com.someone.else"
	notApple := appleNotificationClaims(clock.Now(), valid)
	notApple["iss"] = "https://accounts.google.com"
	testCases := map[string]string{
		"other app":        key.sign(t, otherApp),
		"other issuer":     key.sign(t, notApple),
		"forged signature": newRSATestKey(t, "apple-1").sign(t, appleNotificationClaims(clock.Now(), valid)),
		"no events":        key.sign(t, appleNotificationClaims(clock.Now(), nil)),
		"no subject":       key.sign(t, appleNotificationClaims(clock.Now(), `{"type":"consent-revoked"}`)),
		"malformed events": key.sign(t, appleNotificationClaims(clock.Now(), `{not json`)),
		"empty":            "",
	}
	for name, payload := range testCases {
		t.Run(name, func(t *testing.T) {
			_, err := verifier.VerifyNotification(context.Background(), payload)
			assertInvalidSocialToken(t, err)
		})
	}
}
