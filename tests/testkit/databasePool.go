package testkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"houseflowApi/internal/data/migrations"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Integration tests used to create a database and run every migration for
// each test: about 2.5 s each, more than half of the package's run time. The
// pool migrates a database once and hands it to one test at a time. When the
// test ends the database is reset to the state the migrations left (the
// baseline): collections the test created are dropped, every baseline
// collection gets exactly its baseline documents back, and its options and
// indexes must be unchanged, otherwise the database is dropped instead of
// reused. TestPooledDatabaseResetMatchesAFreshMigration guards the reset.

const pooledDatabasePrefix = "houseflow_test_pool_"

var errPooledDatabaseChanged = errors.New("pooled database structure changed")

type CollectionBaseline struct {
	options   bson.Raw
	indexes   []bson.Raw
	documents []any
}

type DatabaseBaseline map[string]CollectionBaseline

type DatabasePool struct {
	mutex    sync.Mutex
	client   *mongo.Client
	baseline DatabaseBaseline
	free     []string
	created  []string
}

var testDatabases DatabasePool

// AcquireMigratedDatabase returns a migrated database in its baseline state
// for this test only, reached through the test's own client. The test's
// cleanup resets it for the next test.
func AcquireMigratedDatabase(t *testing.T, client *mongo.Client) *mongo.Database {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	name, wasDirty, err := testDatabases.acquire(ctx)
	if err != nil {
		t.Fatalf("migrated test database: %v", err)
	}
	if wasDirty {
		t.Logf("pooled database %s was written after its previous test ended; it was reset again", name)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := testDatabases.release(cleanupCtx, name); err != nil {
			t.Errorf("reset test database: %v", err)
		}
	})
	return client.Database(name)
}

// acquire reports wasDirty when a reused database had changed since its
// reset: a writer of an earlier test outlived that test.
func (pool *DatabasePool) acquire(ctx context.Context) (name string, wasDirty bool, err error) {
	pool.mutex.Lock()
	if pool.client == nil {
		client, err := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("HOUSEFLOW_TEST_MONGO_URI")).SetServerSelectionTimeout(5*time.Second))
		if err != nil {
			pool.mutex.Unlock()
			return "", false, err
		}
		pool.client = client
	}
	if pool.baseline == nil {
		// The first database defines the baseline; the lock keeps every
		// other test waiting for it.
		defer pool.mutex.Unlock()
		name = pool.newDatabaseLocked()
		if err := pool.migrate(ctx, name); err != nil {
			return "", false, err
		}
		baseline, err := captureBaseline(ctx, pool.client.Database(name))
		if err != nil {
			return "", false, err
		}
		pool.baseline = baseline
		return name, false, nil
	}
	if count := len(pool.free); count > 0 {
		name = pool.free[count-1]
		pool.free = pool.free[:count-1]
		pool.mutex.Unlock()
		// A writer that outlived its test would leave the database dirty;
		// the check costs one count per collection and resets it again.
		if clean, err := pool.matchesBaseline(ctx, name); err != nil || !clean {
			if err := pool.reset(ctx, name); err != nil {
				return "", false, err
			}
			return name, true, nil
		}
		return name, false, nil
	}
	// Only tests running at the same time need a further database; it is
	// migrated outside the lock and reset to the shared baseline, so every
	// pooled database holds the same documents.
	name = pool.newDatabaseLocked()
	pool.mutex.Unlock()
	if err := pool.migrate(ctx, name); err != nil {
		return "", false, err
	}
	return name, false, pool.reset(ctx, name)
}

// newDatabaseLocked names a new pooled database; close drops it.
func (pool *DatabasePool) newDatabaseLocked() string {
	name := pooledDatabasePrefix + primitive.NewObjectID().Hex()
	pool.created = append(pool.created, name)
	return name
}

func (pool *DatabasePool) migrate(ctx context.Context, name string) error {
	return migrations.RunAll(ctx, pool.client.Database(name), migrations.AllMigrations())
}

func (pool *DatabasePool) release(ctx context.Context, name string) error {
	if err := pool.reset(ctx, name); err != nil {
		// Never reuse a database whose structure the test changed.
		dropErr := pool.client.Database(name).Drop(ctx)
		if errors.Is(err, errPooledDatabaseChanged) && dropErr == nil {
			return nil
		}
		return errors.Join(err, dropErr)
	}
	pool.mutex.Lock()
	pool.free = append(pool.free, name)
	pool.mutex.Unlock()
	return nil
}

// reset puts a database back into the baseline state, or reports
// errPooledDatabaseChanged when a baseline collection is gone or its
// options or indexes differ.
func (pool *DatabasePool) reset(ctx context.Context, name string) error {
	db := pool.client.Database(name)
	specifications, err := db.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, specification := range specifications {
		present[specification.Name] = true
		baseline, kept := pool.baseline[specification.Name]
		if !kept {
			if err := db.Collection(specification.Name).Drop(ctx); err != nil {
				return err
			}
			continue
		}
		if !bytes.Equal(canonicalBSON(specification.Options, false), baseline.options) {
			return fmt.Errorf("%w: options of %s", errPooledDatabaseChanged, specification.Name)
		}
		indexes, err := collectionIndexes(ctx, db.Collection(specification.Name))
		if err != nil {
			return err
		}
		if !slices.EqualFunc(indexes, baseline.indexes, func(a, b bson.Raw) bool { return bytes.Equal(a, b) }) {
			return fmt.Errorf("%w: indexes of %s", errPooledDatabaseChanged, specification.Name)
		}
	}
	for collection, baseline := range pool.baseline {
		if !present[collection] {
			return fmt.Errorf("%w: %s was dropped", errPooledDatabaseChanged, collection)
		}
		if _, err := db.Collection(collection).DeleteMany(ctx, bson.D{}); err != nil {
			return err
		}
		if len(baseline.documents) > 0 {
			if _, err := db.Collection(collection).InsertMany(ctx, baseline.documents); err != nil {
				return err
			}
		}
	}
	return nil
}

// matchesBaseline is the cheap check before reuse: the same collections with
// the same number of documents.
func (pool *DatabasePool) matchesBaseline(ctx context.Context, name string) (bool, error) {
	db := pool.client.Database(name)
	collections, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil || len(collections) != len(pool.baseline) {
		return false, err
	}
	for _, collection := range collections {
		baseline, kept := pool.baseline[collection]
		if !kept {
			return false, nil
		}
		count, err := db.Collection(collection).CountDocuments(ctx, bson.D{})
		if err != nil || count != int64(len(baseline.documents)) {
			return false, err
		}
	}
	return true, nil
}

func captureBaseline(ctx context.Context, db *mongo.Database) (DatabaseBaseline, error) {
	specifications, err := db.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	baseline := DatabaseBaseline{}
	for _, specification := range specifications {
		collection := db.Collection(specification.Name)
		indexes, err := collectionIndexes(ctx, collection)
		if err != nil {
			return nil, err
		}
		cursor, err := collection.Find(ctx, bson.D{})
		if err != nil {
			return nil, err
		}
		var documents []bson.Raw
		if err := cursor.All(ctx, &documents); err != nil {
			return nil, err
		}
		entry := CollectionBaseline{options: canonicalBSON(specification.Options, false), indexes: indexes, documents: make([]any, len(documents))}
		for index, document := range documents {
			entry.documents[index] = slices.Clone(document)
		}
		baseline[specification.Name] = entry
	}
	return baseline, nil
}

// collectionIndexes lists a collection's index specifications by name.
func collectionIndexes(ctx context.Context, collection *mongo.Collection) ([]bson.Raw, error) {
	cursor, err := collection.Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	var indexes []bson.Raw
	if err := cursor.All(ctx, &indexes); err != nil {
		return nil, err
	}
	slices.SortFunc(indexes, func(a, b bson.Raw) int {
		return bytes.Compare([]byte(a.Lookup("name").StringValue()), []byte(b.Lookup("name").StringValue()))
	})
	for index := range indexes {
		indexes[index] = canonicalBSON(indexes[index], true)
	}
	return indexes, nil
}

// canonicalBSON sorts a document's fields at every level, so documents that
// differ only in field order (migrations write validators from Go maps)
// compare equal. Arrays keep their order; so does an index's "key" document,
// whose order defines the index.
func canonicalBSON(document bson.Raw, index bool) bson.Raw {
	var canonical func(value bson.RawValue, keepOrder bool) any
	canonical = func(value bson.RawValue, keepOrder bool) any {
		switch value.Type {
		case bson.TypeEmbeddedDocument:
			elements, _ := value.Document().Elements()
			fields := make(bson.D, 0, len(elements))
			for _, element := range elements {
				fields = append(fields, bson.E{Key: element.Key(), Value: canonical(element.Value(), index && element.Key() == "key")})
			}
			if !keepOrder {
				slices.SortFunc(fields, func(a, b bson.E) int { return strings.Compare(a.Key, b.Key) })
			}
			return fields
		case bson.TypeArray:
			values, _ := value.Array().Values()
			items := make(bson.A, len(values))
			for position, item := range values {
				items[position] = canonical(item, false)
			}
			return items
		default:
			return value
		}
	}
	if len(document) == 0 {
		return nil
	}
	encoded, err := bson.Marshal(canonical(bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: document}, false))
	if err != nil {
		return slices.Clone(document)
	}
	return encoded
}

// close drops every database the pool created.
func (pool *DatabasePool) close() {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	if pool.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, name := range pool.created {
		_ = pool.client.Database(name).Drop(ctx)
	}
	_ = pool.client.Disconnect(ctx)
}
