package testkit

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"houseflowApi/internal/data/migrations"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// normalizedDocuments renders a collection's documents without generated
// values (object IDs and dates), sorted, so two migrations of the same code
// compare equal.
func normalizedDocuments(t *testing.T, documents []any) []string {
	t.Helper()
	var normalize func(value bson.RawValue) any
	normalize = func(value bson.RawValue) any {
		switch value.Type {
		case bson.TypeObjectID:
			return "<objectId>"
		case bson.TypeDateTime, bson.TypeTimestamp:
			return "<date>"
		case bson.TypeEmbeddedDocument:
			elements, _ := value.Document().Elements()
			fields := make(bson.D, 0, len(elements))
			for _, element := range elements {
				fields = append(fields, bson.E{Key: element.Key(), Value: normalize(element.Value())})
			}
			slices.SortFunc(fields, func(a, b bson.E) int { return strings.Compare(a.Key, b.Key) })
			return fields
		case bson.TypeArray:
			values, _ := value.Array().Values()
			items := make([]any, len(values))
			for index, item := range values {
				items[index] = normalize(item)
			}
			return items
		default:
			return value.String()
		}
	}
	rendered := make([]string, len(documents))
	for index, document := range documents {
		raw := document.(bson.Raw)
		encoded, err := bson.MarshalExtJSON(bson.D{{Key: "document", Value: normalize(bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: raw})}}, true, false)
		if err != nil {
			t.Fatal(err)
		}
		rendered[index] = string(encoded)
	}
	slices.Sort(rendered)
	return rendered
}

func sameBaseline(t *testing.T, actual, expected DatabaseBaseline, exact bool) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("%d collections, want %d", len(actual), len(expected))
	}
	for name, want := range expected {
		got, ok := actual[name]
		if !ok {
			t.Fatalf("collection %s is missing", name)
		}
		if !bytes.Equal(got.options, want.options) || !slices.EqualFunc(got.indexes, want.indexes, func(a, b bson.Raw) bool { return bytes.Equal(a, b) }) {
			t.Fatalf("collection %s: options or indexes differ", name)
		}
		if exact {
			if !slices.EqualFunc(got.documents, want.documents, func(a, b any) bool { return bytes.Equal(a.(bson.Raw), b.(bson.Raw)) }) {
				t.Fatalf("collection %s: documents differ from the baseline", name)
			}
		} else if g, w := normalizedDocuments(t, got.documents), normalizedDocuments(t, want.documents); !slices.Equal(g, w) {
			t.Fatalf("collection %s: documents differ from a fresh migration\n%v\n%v", name, g[:min(3, len(g))], w[:min(3, len(w))])
		}
	}
}

// A reset database is exactly the baseline, and the baseline is what a fresh
// migration of the current code produces (generated IDs and dates aside):
// a test that reuses a pooled database starts where a fresh one would.
func TestPooledDatabaseResetMatchesAFreshMigration(t *testing.T) {
	if os.Getenv("HOUSEFLOW_TEST_MONGO_URI") == "" {
		t.Skip("HOUSEFLOW_TEST_MONGO_URI is required for the test database pool")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	name, _, err := testDatabases.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db := testDatabases.client.Database(name)
	seeded, empty := "", ""
	for collection, baseline := range testDatabases.baseline {
		switch {
		case len(baseline.documents) > 1 && seeded == "":
			seeded = collection
		case len(baseline.documents) == 0 && empty == "":
			empty = collection
		}
	}
	if seeded == "" || empty == "" {
		t.Fatal("the baseline needs a seeded and an empty collection")
	}
	// Everything a test does to the data: insert, update, delete, and a
	// collection of its own. The writes skip the collections' validators;
	// only the reset is under test.
	bypass := struct {
		insert *options.InsertOneOptions
		update *options.UpdateOptions
	}{options.InsertOne().SetBypassDocumentValidation(true), options.Update().SetBypassDocumentValidation(true)}
	first := testDatabases.baseline[seeded].documents[0].(bson.Raw)
	if _, err := db.Collection(seeded).DeleteOne(ctx, bson.D{{Key: "_id", Value: first.Lookup("_id")}}); err != nil {
		t.Fatal(err)
	}
	second := testDatabases.baseline[seeded].documents[1].(bson.Raw)
	if _, err := db.Collection(seeded).UpdateOne(ctx, bson.D{{Key: "_id", Value: second.Lookup("_id")}}, bson.D{{Key: "$set", Value: bson.D{{Key: "changedByTest", Value: true}}}}, bypass.update); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(seeded).InsertOne(ctx, bson.D{{Key: "addedByTest", Value: 1}}, bypass.insert); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(empty).InsertOne(ctx, bson.D{{Key: "addedByTest", Value: 2}}, bypass.insert); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("testOwnCollection").InsertOne(ctx, bson.D{{Key: "x", Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := testDatabases.reset(ctx, name); err != nil {
		t.Fatal(err)
	}
	reset, err := captureBaseline(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	sameBaseline(t, reset, testDatabases.baseline, true)

	fresh := testDatabases.client.Database("houseflow_test_fresh_" + primitive.NewObjectID().Hex())
	t.Cleanup(func() { _ = fresh.Drop(context.Background()) })
	if err := migrations.RunAll(ctx, fresh, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}
	migrated, err := captureBaseline(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	sameBaseline(t, reset, migrated, false)

	// A writer that outlives its test dirties a free database; the next
	// acquire finds it and resets it before handing it out.
	if err := testDatabases.release(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(empty).InsertOne(ctx, bson.D{{Key: "lateWrite", Value: true}}, bypass.insert); err != nil {
		t.Fatal(err)
	}
	reused, wasDirty, err := testDatabases.acquire(ctx)
	if err != nil || reused != name || !wasDirty {
		t.Fatalf("reuse: %s %v", reused, err)
	}
	if count, _ := db.Collection(empty).CountDocuments(ctx, bson.D{}); count != 0 {
		t.Fatal("a dirty pooled database was handed out")
	}

	// A test that changes the structure (here an index) loses the database:
	// it is dropped, not reused.
	if _, err := db.Collection(empty).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "addedByTest", Value: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := testDatabases.release(ctx, name); err != nil {
		t.Fatal(err)
	}
	testDatabases.mutex.Lock()
	reusable := slices.Contains(testDatabases.free, name)
	testDatabases.mutex.Unlock()
	names, _ := testDatabases.client.ListDatabaseNames(ctx, bson.D{{Key: "name", Value: name}})
	if reusable || len(names) != 0 {
		t.Fatal("a database with a changed structure must be dropped")
	}
}
