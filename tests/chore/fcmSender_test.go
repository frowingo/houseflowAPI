package chore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	"houseflowApi/internal/infrastructure/push"
)

// fcmWireMessage is the part of the FCM v1 request the tests read.
type fcmWireMessage struct {
	Message struct {
		Token        string `json:"token"`
		Notification struct{ Title, Body string }
		Data         map[string]string `json:"data"`
		Android      struct {
			Priority     string `json:"priority"`
			TTL          string `json:"ttl"`
			Notification *struct {
				Tag string `json:"tag"`
			} `json:"notification"`
		} `json:"android"`
		APNS struct {
			Headers map[string]string `json:"headers"`
			Payload struct {
				Aps struct {
					Sound string `json:"sound"`
				} `json:"aps"`
			} `json:"payload"`
		} `json:"apns"`
	} `json:"message"`
}

func TestFCMSendBuildsMessageForBothPlatforms(t *testing.T) {
	var received fcmWireMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`))
	}))
	defer server.Close()

	sender := push.NewFCMSenderWithClient(server.Client(), server.URL)
	before := time.Now()
	err := sender.Send(context.Background(), "token-1", notificationAbstract.PushMessage{
		Title: "Başlık", Body: "Mesaj", Data: map[string]string{"type": "announcement"},
		CollapseID: "notification-1", TTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := received.Message
	if message.Token != "token-1" || message.Notification.Title != "Başlık" || message.Notification.Body != "Mesaj" ||
		message.Data["type"] != "announcement" || message.Android.Priority != "high" || message.APNS.Payload.Aps.Sound != "default" {
		t.Fatalf("unexpected message %+v", message)
	}
	if message.Android.TTL != "86400s" || message.Android.Notification == nil || message.Android.Notification.Tag != "notification-1" {
		t.Fatalf("android %+v", message.Android)
	}
	headers := message.APNS.Headers
	if headers["apns-push-type"] != "alert" || headers["apns-priority"] != "10" || headers["apns-collapse-id"] != "notification-1" {
		t.Fatalf("apns headers %v", headers)
	}
	expiration, err := strconv.ParseInt(headers["apns-expiration"], 10, 64)
	if err != nil || expiration < before.Add(24*time.Hour).Unix() || expiration > time.Now().Add(24*time.Hour).Unix() {
		t.Fatalf("apns-expiration = %q", headers["apns-expiration"])
	}
}

func TestFCMSendWithoutCollapseOrTTLLeavesThemOut(t *testing.T) {
	var received fcmWireMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
	}))
	defer server.Close()
	if err := push.NewFCMSenderWithClient(server.Client(), server.URL).Send(context.Background(), "token", notificationAbstract.PushMessage{}); err != nil {
		t.Fatal(err)
	}
	message := received.Message
	if message.Android.TTL != "" || message.Android.Notification != nil ||
		message.APNS.Headers["apns-collapse-id"] != "" || message.APNS.Headers["apns-expiration"] != "" {
		t.Fatalf("unexpected message %+v", message)
	}
}

func TestFCMSendClassifiesFailures(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		body       string
		invalid    bool
		rejected   bool
		throttle   time.Duration
	}{
		{name: "unregistered", status: http.StatusNotFound, body: `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, invalid: true},
		{name: "not found", status: http.StatusNotFound, body: `{"error":{"status":"NOT_FOUND"}}`, invalid: true},
		{name: "other project", status: http.StatusForbidden, body: `{"error":{"status":"PERMISSION_DENIED","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, invalid: true},
		{name: "bad payload", status: http.StatusBadRequest, body: `{"error":{"status":"INVALID_ARGUMENT","details":[{"errorCode":"INVALID_ARGUMENT"}]}}`, rejected: true},
		{name: "quota with delay", status: http.StatusTooManyRequests, retryAfter: "30", body: `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, throttle: 30 * time.Second},
		{name: "unavailable with delay", status: http.StatusServiceUnavailable, retryAfter: "5", body: `{"error":{"status":"UNAVAILABLE"}}`, throttle: 5 * time.Second},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{"error":{"status":"UNAVAILABLE"}}`},
		{name: "internal", status: http.StatusInternalServerError, body: `{"error":{"status":"INTERNAL"}}`},
		{name: "expired access token", status: http.StatusUnauthorized, body: `{"error":{"status":"UNAUTHENTICATED"}}`},
	}
	for _, testCase := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if testCase.retryAfter != "" {
				w.Header().Set("Retry-After", testCase.retryAfter)
			}
			w.WriteHeader(testCase.status)
			_, _ = w.Write([]byte(testCase.body))
		}))
		err := push.NewFCMSenderWithClient(server.Client(), server.URL).Send(context.Background(), "token", notificationAbstract.PushMessage{})
		server.Close()
		if err == nil {
			t.Fatalf("%s: expected an error", testCase.name)
		}
		if errors.Is(err, notificationAbstract.ErrInvalidPushToken) != testCase.invalid {
			t.Fatalf("%s: invalid token? err = %v", testCase.name, err)
		}
		if errors.Is(err, notificationAbstract.ErrPushRejected) != testCase.rejected {
			t.Fatalf("%s: rejected? err = %v", testCase.name, err)
		}
		var throttled *notificationAbstract.PushThrottledError
		isThrottled := errors.As(err, &throttled)
		if isThrottled != (testCase.throttle > 0) || (isThrottled && throttled.RetryAfter != testCase.throttle) {
			t.Fatalf("%s: throttled? err = %v", testCase.name, err)
		}
	}
}
