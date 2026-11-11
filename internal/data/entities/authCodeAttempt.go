package entities

import "time"

// AuthCodeAttempt counts the wrong guesses at an emailed code for one
// purpose, email and code window. Id is "<purpose>|<email>|<windowUnix>";
// a TTL index on expiresAt removes it once the window is long gone.
type AuthCodeAttempt struct {
	Id        string    `bson:"_id" json:"-"`
	Count     int       `bson:"count" json:"-"`
	ExpiresAt time.Time `bson:"expiresAt" json:"-"`
}
