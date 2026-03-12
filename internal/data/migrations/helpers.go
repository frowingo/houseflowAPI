package migrations

import "go.mongodb.org/mongo-driver/mongo"

// isCollectionExistsError returns true when a CreateCollection call fails
// because the collection already exists (error code 48 — NamespaceExists).
func isCollectionExistsError(err error) bool {
	if err == nil {
		return false
	}
	cmdErr, ok := err.(mongo.CommandError)
	return ok && cmdErr.Code == 48
}
