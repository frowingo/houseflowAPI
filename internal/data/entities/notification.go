package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type NotificationType string

const (
	AnnouncementNotification  NotificationType = "announcement"
	ChoreReminderNotification NotificationType = "chore_reminder"
)

// Notification is stored once per house and shown in the in-app panel.
// RecipientUserIds narrows it to some members; empty means the whole house.
// When Localized is set, Title and Message are localization keys rendered
// with Args in the reader's language; otherwise they are shown as written.
// Read state is kept by the clients.
type Notification struct {
	Id               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Type             NotificationType   `bson:"type" json:"type"`
	HouseId          string             `bson:"houseId" json:"houseId"`
	SenderUserId     string             `bson:"senderUserId" json:"senderUserId"`
	RecipientUserIds []string           `bson:"recipientUserIds,omitempty" json:"-"`
	ChoreId          *string            `bson:"choreId,omitempty" json:"choreId,omitempty"`
	Title            string             `bson:"title" json:"title"`
	Message          string             `bson:"message" json:"message"`
	Localized        bool               `bson:"localized" json:"-"`
	Args             []string           `bson:"args,omitempty" json:"-"`
	CreatedAt        time.Time          `bson:"createdAt" json:"createdAt"`
	ExpireAt         time.Time          `bson:"expireAt" json:"-"`
}
