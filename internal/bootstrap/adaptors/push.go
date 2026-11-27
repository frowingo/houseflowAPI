package adaptors

import (
	"context"
	"log"

	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/infrastructure/push"
)

// NewPushSender returns the FCM sender, or a sender that drops push
// notifications when Firebase is not configured.
func NewPushSender(ctx context.Context, firebaseConfig config.ConfigFirebase) (notificationAbstract.PushSender, error) {
	if !firebaseConfig.Enabled() {
		log.Println("push notifications disabled: FIREBASE_CREDENTIALS is not set")
		return push.NoopSender{}, nil
	}
	credentials, err := firebaseConfig.Credentials()
	if err != nil {
		return nil, err
	}
	return push.NewFCMSender(ctx, credentials)
}
