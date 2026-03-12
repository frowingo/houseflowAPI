package migrations

import "houseflowApi/external/migration"

// AllMigrations returns every migration in the order they must be applied.
// To add a new migration: create a new file in this package and append its
// instance to the slice below.
func AllMigrations() []migration.Migration {
	return []migration.Migration{
		// --- Collection Init ---
		&createUserCollection{},
		&createHouseCollection{},
		&createChoreCollection{},
		&createChoreStatusHistoryCollection{},
		&createNotificationCollection{},
		&createAnnouncementCollection{},
		// --- Index ---
		&userIndexes{},
		&houseIndexes{},
		&choreIndexes{},
		&choreStatusHistoryIndexes{},
		&announcementIndexes{},
		&notificationIndexes{},
		// --- Schema Transform ---
		&notificationBsonFix{},
		&announcementBsonFix{},
	}
}
