package account

import (
	"testing"
	"time"

	localizationCommands "houseflowApi/internal/application/localization/commands"
	localizationQueries "houseflowApi/internal/application/localization/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
)

func TestLocalizationCommandsAndQueriesUseAtomicWritesAndCache(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)

	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, localizationCommands.InsertLocalizationLanguageCommand{
		Prefix: "DE", Name: "German", NativeName: "Deutsch",
		IsActive: true, Image: "de.png",
	}); err != nil {
		t.Fatal(err)
	}
	language, err := cqrs.Send[[]dtos.LocalizationLanguageResponseModel](fixture.Ctx, fixture.Sender, localizationQueries.GetLanguageQuery{Prefix: " de "})
	if err != nil {
		t.Fatal(err)
	}
	if len(language) != 1 || language[0].Prefix != "de" {
		t.Fatalf("language response=%+v, want active de language", language)
	}

	values := []localizationCommands.LocalizationValue{
		{Type: "PLAINTEXT", Language: "EN", Key: "phase5.second", Value: "Second"},
		{Type: "plaintext", Language: "en", Key: "phase5.first", Value: "First"},
	}
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, localizationCommands.InsertLocalizationsCommand{
		Localizations: values,
	}); err != nil {
		t.Fatal(err)
	}

	plaintexts, err := cqrs.Send[[]dtos.LocalizationPlaintextResponseModel](fixture.Ctx, fixture.Sender, localizationQueries.GetPlaintextsQuery{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plaintexts) != 2 || plaintexts[0].Key != "phase5.first" || plaintexts[1].Key != "phase5.second" {
		t.Fatalf("plaintext response=%+v, want sorted phase 5 values", plaintexts)
	}

	if _, err := fixture.DB.Collection("localization").DeleteMany(fixture.Ctx, bson.M{"key": bson.M{"$in": bson.A{"phase5.first", "phase5.second"}}}); err != nil {
		t.Fatal(err)
	}
	cachedPlaintexts, err := cqrs.Send[[]dtos.LocalizationPlaintextResponseModel](fixture.Ctx, fixture.Sender, localizationQueries.GetPlaintextsQuery{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cachedPlaintexts) != 2 {
		t.Fatalf("cached plaintext count=%d, want 2", len(cachedPlaintexts))
	}

	upsertKey := "phase5.upsert"
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, localizationCommands.InsertLocalizationsCommand{
		Localizations: []localizationCommands.LocalizationValue{
			{Type: "plaintext", Language: "en", Key: upsertKey, Value: "First value"},
			{Type: "PLAINTEXT", Language: "EN", Key: upsertKey, Value: "Second value"},
		},
	}); err != nil {
		t.Fatalf("same key twice in one batch: %v", err)
	}
	first := findLocalization(t, fixture, upsertKey)
	if first.Value != "Second value" {
		t.Fatalf("value=%q, want last value of the batch", first.Value)
	}
	time.Sleep(10 * time.Millisecond) // Mongo dates are millisecond precision

	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, localizationCommands.InsertLocalizationsCommand{
		Localizations: []localizationCommands.LocalizationValue{
			{Type: "plaintext", Language: "en", Key: upsertKey, Value: "Updated value"},
		},
	}); err != nil {
		t.Fatalf("existing key upsert: %v", err)
	}
	count, err := fixture.DB.Collection("localization").CountDocuments(fixture.Ctx, bson.M{"key": upsertKey})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("localization count=%d, want 1 after upsert", count)
	}
	updated := findLocalization(t, fixture, upsertKey)
	if updated.Value != "Updated value" || updated.Id != first.Id {
		t.Fatalf("updated=%+v, want same document with new value", updated)
	}
	if !updated.CreatedOn.Equal(first.CreatedOn) || !updated.UpdatedOn.After(first.UpdatedOn) {
		t.Fatalf("createdOn %v->%v updatedOn %v->%v, want createdOn kept and updatedOn advanced",
			first.CreatedOn, updated.CreatedOn, first.UpdatedOn, updated.UpdatedOn)
	}

	plaintexts, err = cqrs.Send[[]dtos.LocalizationPlaintextResponseModel](fixture.Ctx, fixture.Sender, localizationQueries.GetPlaintextsQuery{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	cachedValue := ""
	for _, plaintext := range plaintexts {
		if plaintext.Key == upsertKey {
			cachedValue = plaintext.Value
		}
	}
	if cachedValue != "Updated value" {
		t.Fatalf("cached value=%q, want updated value", cachedValue)
	}
}

func findLocalization(t *testing.T, fixture *appkit.ConcurrencyFixture, key string) entities.Localization {
	t.Helper()
	var localization entities.Localization
	if err := fixture.DB.Collection("localization").FindOne(fixture.Ctx, bson.M{"key": key}).Decode(&localization); err != nil {
		t.Fatal(err)
	}
	return localization
}
