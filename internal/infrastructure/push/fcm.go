package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	notificationAbstract "houseflowApi/internal/application/notification/abstract"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	fcmScope       = "https://www.googleapis.com/auth/firebase.messaging"
	fcmEndpoint    = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
	fcmSendTimeout = 10 * time.Second
)

// FCMSender sends push notifications to Android and iOS devices through the
// Firebase Cloud Messaging HTTP v1 API. iOS delivery goes through the APNs
// key uploaded to the Firebase project.
type FCMSender struct {
	client   *http.Client
	endpoint string
}

// NewFCMSender authenticates with a Firebase service account JSON.
func NewFCMSender(ctx context.Context, credentialsJSON []byte) (*FCMSender, error) {
	credentials, err := google.CredentialsFromJSON(ctx, credentialsJSON, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("read firebase credentials: %w", err)
	}
	if credentials.ProjectID == "" {
		return nil, errors.New("firebase credentials have no project_id")
	}
	client := oauth2.NewClient(ctx, credentials.TokenSource)
	client.Timeout = fcmSendTimeout
	return NewFCMSenderWithClient(client, fmt.Sprintf(fcmEndpoint, credentials.ProjectID)), nil
}

// NewFCMSenderWithClient sends through an already authenticated client to an
// endpoint; tests point it at a local server.
func NewFCMSenderWithClient(client *http.Client, endpoint string) *FCMSender {
	return &FCMSender{client: client, endpoint: endpoint}
}

type fcmRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
	Android      fcmAndroid        `json:"android"`
	APNS         fcmAPNS           `json:"apns"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type fcmAndroid struct {
	Priority     string                  `json:"priority"`
	TTL          string                  `json:"ttl,omitempty"`
	Notification *fcmAndroidNotification `json:"notification,omitempty"`
}

// Tag replaces a shown notification with the same tag instead of adding one.
type fcmAndroidNotification struct {
	Tag string `json:"tag"`
}

type fcmAPNS struct {
	Headers map[string]string `json:"headers,omitempty"`
	Payload fcmAPNSPayload    `json:"payload"`
}

type fcmAPNSPayload struct {
	Aps fcmAps `json:"aps"`
}

type fcmAps struct {
	Sound string `json:"sound"`
}

type fcmErrorResponse struct {
	Error struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			ErrorCode string `json:"errorCode"`
			// FieldViolations come with a google.rpc.BadRequest detail and
			// name the request field FCM refused.
			FieldViolations []struct {
				Field string `json:"field"`
			} `json:"fieldViolations"`
		} `json:"details"`
	} `json:"error"`
}

// Send reports a token the device gave up as ErrInvalidPushToken, a message
// FCM refuses for good as ErrPushRejected, and a request to slow down as a
// PushThrottledError; any other error is worth retrying.
func (s *FCMSender) Send(ctx context.Context, token string, message notificationAbstract.PushMessage) error {
	body, err := json.Marshal(fcmRequest{Message: buildMessage(token, message, time.Now())})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}

	var failure fcmErrorResponse
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&failure)
	if isInvalidToken(response.StatusCode, failure) {
		return notificationAbstract.ErrInvalidPushToken
	}
	err = fmt.Errorf("fcm send failed: %d %s %s", response.StatusCode, failure.Error.Status, failure.Error.Message)
	switch {
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable:
		if delay, ok := retryAfter(response.Header.Get("Retry-After"), time.Now()); ok {
			return &notificationAbstract.PushThrottledError{RetryAfter: delay, Err: err}
		}
		return err
	case response.StatusCode >= 500, response.StatusCode == http.StatusUnauthorized:
		// A server error, or an access token that expired in flight.
		return err
	case response.StatusCode >= 400:
		return fmt.Errorf("%w: %w", notificationAbstract.ErrPushRejected, err)
	default:
		return err
	}
}

// buildMessage asks both platforms to show one notification per CollapseID
// and to give up on an offline device after TTL.
func buildMessage(token string, message notificationAbstract.PushMessage, now time.Time) fcmMessage {
	result := fcmMessage{
		Token:        token,
		Notification: fcmNotification{Title: message.Title, Body: message.Body},
		Data:         message.Data,
		Android:      fcmAndroid{Priority: "high"},
		APNS: fcmAPNS{
			Headers: map[string]string{"apns-push-type": "alert", "apns-priority": "10"},
			Payload: fcmAPNSPayload{Aps: fcmAps{Sound: "default"}},
		},
	}
	if message.CollapseID != "" {
		result.Android.Notification = &fcmAndroidNotification{Tag: message.CollapseID}
		// APNs limits the collapse id to 64 bytes.
		if len(message.CollapseID) <= 64 {
			result.APNS.Headers["apns-collapse-id"] = message.CollapseID
		}
	}
	if message.TTL > 0 {
		result.Android.TTL = strconv.FormatInt(int64(message.TTL/time.Second), 10) + "s"
		result.APNS.Headers["apns-expiration"] = strconv.FormatInt(now.Add(message.TTL).Unix(), 10)
	}
	return result
}

// retryAfter reads a Retry-After header in seconds or as an HTTP date.
func retryAfter(header string, now time.Time) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if at, err := http.ParseTime(header); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay, true
		}
		return 0, true
	}
	return 0, false
}

// isInvalidToken reports a token that can never receive a push: the device
// unregistered it, it belongs to another Firebase project, or it is not an
// FCM token at all (for example an APNs device token the app sent instead).
// INVALID_ARGUMENT alone is not enough, as a bad payload, which must not
// remove every token, has it too; only a violation on message.token counts.
func isInvalidToken(statusCode int, failure fcmErrorResponse) bool {
	for _, detail := range failure.Error.Details {
		// SENDER_ID_MISMATCH: the token belongs to another Firebase project.
		if detail.ErrorCode == "UNREGISTERED" || detail.ErrorCode == "SENDER_ID_MISMATCH" {
			return true
		}
		for _, violation := range detail.FieldViolations {
			if violation.Field == "message.token" {
				return true
			}
		}
	}
	return statusCode == http.StatusNotFound && failure.Error.Status == "NOT_FOUND"
}

// NoopSender is used when FCM is not configured (local runs, tests).
type NoopSender struct{}

func (NoopSender) Send(context.Context, string, notificationAbstract.PushMessage) error { return nil }

var (
	_ notificationAbstract.PushSender = (*FCMSender)(nil)
	_ notificationAbstract.PushSender = NoopSender{}
)
