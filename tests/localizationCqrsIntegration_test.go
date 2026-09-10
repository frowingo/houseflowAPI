package tests

import (
	"testing"

	localizationCommands "houseflowApi/internal/application/localization/commands"
	localizationQueries "houseflowApi/internal/application/localization/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

func TestLocalizationCommandsAndQueriesUseAtomicWritesAndCache(t *testing.T) {
	fixture := newConcurrencyFixture(t)

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, localizationCommands.InsertLocalizationLanguageCommand{
		Prefix: "DE", Name: "German", NativeName: "Deutsch",
		IsActive: true, Image: "de.png",
	}); err != nil {
		t.Fatal(err)
	}
	language, err := cqrs.Send[[]dtos.LocalizationLanguageResponseModel](fixture.ctx, fixture.sender, localizationQueries.GetLanguageQuery{Prefix: " de "})
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
	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, localizationCommands.InsertLocalizationsCommand{
		Localizations: values,
	}); err != nil {
		t.Fatal(err)
	}

	plaintexts, err := cqrs.Send[[]dtos.LocalizationPlaintextResponseModel](fixture.ctx, fixture.sender, localizationQueries.GetPlaintextsQuery{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plaintexts) != 2 || plaintexts[0].Key != "phase5.first" || plaintexts[1].Key != "phase5.second" {
		t.Fatalf("plaintext response=%+v, want sorted phase 5 values", plaintexts)
	}

	if _, err := fixture.db.Collection("localization").DeleteMany(fixture.ctx, bson.M{"key": bson.M{"$in": bson.A{"phase5.first", "phase5.second"}}}); err != nil {
		t.Fatal(err)
	}
	cachedPlaintexts, err := cqrs.Send[[]dtos.LocalizationPlaintextResponseModel](fixture.ctx, fixture.sender, localizationQueries.GetPlaintextsQuery{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cachedPlaintexts) != 2 {
		t.Fatalf("cached plaintext count=%d, want 2", len(cachedPlaintexts))
	}

	duplicateKey := "phase5.transaction-rollback"
	_, err = cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, localizationCommands.InsertLocalizationsCommand{
		Localizations: []localizationCommands.LocalizationValue{
			{Type: "message", Language: "en", Key: duplicateKey, Value: "First value"},
			{Type: "message", Language: "en", Key: duplicateKey, Value: "Duplicate value"},
		},
	})
	if !helpers.IsApplicationError(err, "localization.error.duplicate_key") {
		t.Fatalf("duplicate error=%v, want localization conflict", err)
	}
	count, err := fixture.db.Collection("localization").CountDocuments(fixture.ctx, bson.M{"key": duplicateKey})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back localization count=%d, want 0", count)
	}
}
