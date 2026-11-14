package chore

import (
	"errors"
	"strings"
	"testing"
	"time"

	"houseflowApi/external/validator"
	choreCommands "houseflowApi/internal/application/chore/commands"
	choreCatalogCommands "houseflowApi/internal/application/choreCatalog/commands"
	choreCatalogQueries "houseflowApi/internal/application/choreCatalog/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	normalRole     = int(entities.Normal)
	superAdminRole = int(entities.SuperAdmin)
)

func catalogItem(title string, category entities.ChoreCategory) dtos.ChoreCatalogModel {
	return dtos.ChoreCatalogModel{
		Title: title, Description: title + " description", RecurringInterval: 7,
		Level: entities.Easy, Category: category,
	}
}

// systemCatalogItem uses the localization keys of a seeded system entry.
func systemCatalogItem(key string, category entities.ChoreCategory) dtos.ChoreCatalogModel {
	item := catalogItem(entities.SystemChoreCatalogKeyPrefix+key+".title", category)
	item.Description = entities.SystemChoreCatalogKeyPrefix + key + ".description"
	item.IsSystem = true
	return item
}

// clearCatalog removes the seeded system entries so a test controls the whole
// catalog; their translations stay.
func clearCatalog(t *testing.T, fixture *appkit.ConcurrencyFixture) {
	t.Helper()
	if _, err := fixture.DB.Collection("ChoreCatalog").DeleteMany(fixture.Ctx, bson.M{}); err != nil {
		t.Fatal(err)
	}
}

func upsertCatalog(
	fixture *appkit.ConcurrencyFixture, userID string, role int, items ...dtos.ChoreCatalogModel,
) ([]dtos.ChoreCatalogModel, error) {
	return cqrs.Send[[]dtos.ChoreCatalogModel](fixture.Ctx, fixture.Sender, choreCatalogCommands.UpsertChoreCatalogCommand{
		Items: items, RequesterID: userID, RequesterRole: role,
	})
}

func mustUpsertCatalog(
	t *testing.T, fixture *appkit.ConcurrencyFixture, userID string, role int, items ...dtos.ChoreCatalogModel,
) []dtos.ChoreCatalogModel {
	t.Helper()
	result, err := upsertCatalog(fixture, userID, role, items...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func listCatalog(t *testing.T, fixture *appkit.ConcurrencyFixture, userID string) []dtos.ChoreCatalogModel {
	t.Helper()
	return listCatalogWithTokenLanguage(t, fixture, userID, "")
}

func listCatalogWithTokenLanguage(
	t *testing.T, fixture *appkit.ConcurrencyFixture, userID, tokenLanguage string,
) []dtos.ChoreCatalogModel {
	t.Helper()
	result, err := cqrs.Send[[]dtos.ChoreCatalogModel](fixture.Ctx, fixture.Sender, choreCatalogQueries.ListChoreCatalogQuery{
		RequesterID: userID, Language: tokenLanguage,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func catalogTitles(items []dtos.ChoreCatalogModel) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func catalogCount(t *testing.T, fixture *appkit.ConcurrencyFixture) int64 {
	t.Helper()
	count, err := fixture.DB.Collection("ChoreCatalog").CountDocuments(fixture.Ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func requireApplicationError(t *testing.T, err error, kind helpers.ErrorKind, key string) {
	t.Helper()
	var applicationError *helpers.ApplicationError
	if !errors.As(err, &applicationError) || applicationError.Kind != kind || applicationError.Key != key {
		t.Fatalf("err = %v, want kind %d key %s", err, kind, key)
	}
}

func TestChoreCatalogUpsertInsertsUpdatesSoftDeletesAndLists(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	admin := fixture.SeedUser(t, "correct-password").Id.Hex()
	owner := fixture.SeedUser(t, "correct-password").Id.Hex()
	other := fixture.SeedUser(t, "correct-password").Id.Hex()
	clearCatalog(t, fixture)

	system := mustUpsertCatalog(t, fixture, admin, superAdminRole,
		systemCatalogItem("kitchen.wash_dishes", entities.ChoreCategoryKitchen))
	if len(system) != 1 || !system[0].IsSystem || system[0].UserId != entities.SystemUserId {
		t.Fatalf("system insert = %+v, want a SYSTEM entry", system)
	}

	created := mustUpsertCatalog(t, fixture, owner, normalRole,
		catalogItem("Feed cat", entities.ChoreCategoryPets),
		catalogItem("Water plants", entities.ChoreCategoryPlantsBalcony),
	)
	mustUpsertCatalog(t, fixture, other, normalRole, catalogItem("Other user entry", entities.ChoreCategoryRooms))
	if len(created) != 2 || created[0].Id == "" || created[0].UserId != owner || created[0].IsSystem {
		t.Fatalf("owner insert = %+v", created)
	}

	// One request updates an entry and inserts a new one; the response keeps the request order.
	update := created[0]
	update.Title = "Feed the cat"
	update.Level = entities.Medium
	update.UserId, update.IsSystem = other, true // server-owned fields are ignored on update
	mixed := mustUpsertCatalog(t, fixture, owner, normalRole, update, catalogItem("Take out trash", entities.ChoreCategoryTrashRecycling))
	if len(mixed) != 2 || mixed[0].Id != created[0].Id || mixed[0].Title != "Feed the cat" || mixed[0].Level != entities.Medium {
		t.Fatalf("mixed upsert first item = %+v", mixed)
	}
	if mixed[0].UserId != owner || mixed[0].IsSystem {
		t.Fatalf("update changed ownership: %+v", mixed[0])
	}
	if !mixed[0].CreatedOn.Equal(created[0].CreatedOn.Time) || mixed[0].UpdatedOn.Before(created[0].UpdatedOn.Time) {
		t.Fatalf("update timestamps = %v/%v, created %v", mixed[0].CreatedOn, mixed[0].UpdatedOn, created[0].CreatedOn)
	}
	if mixed[1].Id == "" || mixed[1].Title != "Take out trash" {
		t.Fatalf("mixed upsert second item = %+v", mixed[1])
	}

	// Sorted by category, then title; other users' entries are not listed.
	want := []string{"Wash the dishes", "Feed the cat", "Take out trash", "Water plants"}
	if got := catalogTitles(listCatalog(t, fixture, owner)); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("owner list = %v, want %v", got, want)
	}

	deleteItem := created[1]
	deleteItem.IsDeleted = true
	deleteItem.Title = "Ignored on delete"
	deleted := mustUpsertCatalog(t, fixture, owner, normalRole, deleteItem)
	if len(deleted) != 1 || !deleted[0].IsDeleted || deleted[0].Title != "Water plants" {
		t.Fatalf("soft delete result = %+v", deleted)
	}
	want = []string{"Wash the dishes", "Feed the cat", "Take out trash"}
	if got := catalogTitles(listCatalog(t, fixture, owner)); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("owner list after delete = %v, want %v", got, want)
	}
	if got := catalogTitles(listCatalog(t, fixture, other)); strings.Join(got, "|") != "Wash the dishes|Other user entry" {
		t.Fatalf("other list = %v", got)
	}
}

func TestChoreCatalogUpsertRejectsMissingDeletedOrForeignEntriesAtomically(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password").Id.Hex()
	other := fixture.SeedUser(t, "correct-password").Id.Hex()
	clearCatalog(t, fixture)

	owned := mustUpsertCatalog(t, fixture, owner, normalRole, catalogItem("Owned", entities.ChoreCategoryRooms))[0]
	foreign := mustUpsertCatalog(t, fixture, other, normalRole, catalogItem("Foreign", entities.ChoreCategoryRooms))[0]
	gone := mustUpsertCatalog(t, fixture, owner, normalRole, catalogItem("Gone", entities.ChoreCategoryRooms))[0]
	gone.IsDeleted = true
	mustUpsertCatalog(t, fixture, owner, normalRole, gone)

	missing := catalogItem("Missing", entities.ChoreCategoryRooms)
	missing.Id = primitive.NewObjectID().Hex()
	deletedAgain := gone
	deletedAgain.IsDeleted = false
	for name, target := range map[string]dtos.ChoreCatalogModel{
		"missing": missing, "deleted": deletedAgain, "foreign": foreign,
	} {
		before := catalogCount(t, fixture)
		renamed := owned
		renamed.Title = "Renamed before failure"
		_, err := upsertCatalog(fixture, owner, normalRole,
			catalogItem("Inserted before failure", entities.ChoreCategoryRooms), renamed, target)
		requireApplicationError(t, err, helpers.ErrorKindNotFound, "choreCatalog.error.not_found")
		if after := catalogCount(t, fixture); after != before {
			t.Fatalf("%s: insert was not rolled back (%d -> %d)", name, before, after)
		}
		if got := catalogTitles(listCatalog(t, fixture, owner)); strings.Join(got, "|") != "Owned" {
			t.Fatalf("%s: update was not rolled back, list = %v", name, got)
		}
	}

	insertDeleted := catalogItem("Deleted insert", entities.ChoreCategoryRooms)
	insertDeleted.IsDeleted = true
	_, err := upsertCatalog(fixture, owner, normalRole, insertDeleted)
	requireApplicationError(t, err, helpers.ErrorKindBadRequest, "choreCatalog.error.delete_requires_id")
}

func TestChoreCatalogSystemEntriesRequireSuperAdmin(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	admin := fixture.SeedUser(t, "correct-password").Id.Hex()
	user := fixture.SeedUser(t, "correct-password").Id.Hex()
	clearCatalog(t, fixture)

	systemItem := systemCatalogItem("floors_general.mop_floors", entities.ChoreCategoryFloorsGeneral)
	for _, role := range []int{normalRole, int(entities.Admin)} {
		_, err := upsertCatalog(fixture, user, role, systemItem)
		requireApplicationError(t, err, helpers.ErrorKindForbidden, "choreCatalog.error.system_forbidden")
	}
	if count := catalogCount(t, fixture); count != 0 {
		t.Fatalf("forbidden system insert stored %d entries", count)
	}

	// A system entry keeps localization keys; plain text is rejected.
	plain := systemItem
	plain.Title = "Mop floors"
	_, err := upsertCatalog(fixture, admin, superAdminRole, plain)
	requireApplicationError(t, err, helpers.ErrorKindBadRequest, "choreCatalog.error.system_requires_keys")
	plain = systemItem
	plain.Description = "Mop every floor"
	_, err = upsertCatalog(fixture, admin, superAdminRole, plain)
	requireApplicationError(t, err, helpers.ErrorKindBadRequest, "choreCatalog.error.system_requires_keys")

	system := mustUpsertCatalog(t, fixture, admin, superAdminRole, systemItem)[0]
	if system.Title != systemItem.Title || system.Description != systemItem.Description {
		t.Fatalf("upsert response = %+v, want the stored keys", system)
	}
	localizedCopy := system
	localizedCopy.Title = "Mop the floors"
	_, err = upsertCatalog(fixture, admin, superAdminRole, localizedCopy)
	requireApplicationError(t, err, helpers.ErrorKindBadRequest, "choreCatalog.error.system_requires_keys")

	change := system
	change.Title = entities.SystemChoreCatalogKeyPrefix + "floors_general.deep_clean.title"
	for _, role := range []int{normalRole, int(entities.Admin)} {
		_, err := upsertCatalog(fixture, user, role, change)
		requireApplicationError(t, err, helpers.ErrorKindForbidden, "choreCatalog.error.system_forbidden")
	}

	updated := mustUpsertCatalog(t, fixture, admin, superAdminRole, change)[0]
	if updated.Title != change.Title || updated.UserId != entities.SystemUserId {
		t.Fatalf("super admin update = %+v", updated)
	}
	updated.IsDeleted = true
	mustUpsertCatalog(t, fixture, admin, superAdminRole, updated)
	if got := listCatalog(t, fixture, user); len(got) != 0 {
		t.Fatalf("deleted system entry is still listed: %+v", got)
	}
}

func TestChoreCatalogEntriesAreRemovedWithTheAccount(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	admin := fixture.SeedUser(t, "correct-password").Id.Hex()
	user := fixture.SeedUser(t, "correct-password").Id.Hex()
	clearCatalog(t, fixture)
	mustUpsertCatalog(t, fixture, admin, superAdminRole,
		systemCatalogItem("shopping_home.pay_bills", entities.ChoreCategoryShoppingHome))
	mustUpsertCatalog(t, fixture, user, normalRole, catalogItem("Personal entry", entities.ChoreCategoryShoppingHome))

	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, userCommands.DeleteUserCommand{UserID: user}); err != nil {
		t.Fatal(err)
	}
	remaining, err := fixture.DB.Collection("ChoreCatalog").CountDocuments(fixture.Ctx, bson.M{"userId": user})
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || catalogCount(t, fixture) != 1 {
		t.Fatalf("after account removal user entries = %d, total = %d; want 0 and the system entry", remaining, catalogCount(t, fixture))
	}
}

func TestChoreCatalogModelValidation(t *testing.T) {
	v := validator.NewValidator()
	valid := catalogItem("Valid", entities.ChoreCategoryToilet)
	if err := v.Validate(dtos.ChoreCatalogUpsertModel{Items: []dtos.ChoreCatalogModel{valid}}); err != nil {
		t.Fatalf("valid item rejected: %v", err)
	}

	invalid := map[string]func(*dtos.ChoreCatalogModel){
		"category zero":     func(m *dtos.ChoreCatalogModel) { m.Category = 0 },
		"category ten":      func(m *dtos.ChoreCatalogModel) { m.Category = 10 },
		"level":             func(m *dtos.ChoreCatalogModel) { m.Level = 15 },
		"short title":       func(m *dtos.ChoreCatalogModel) { m.Title = "ab" },
		"long description":  func(m *dtos.ChoreCatalogModel) { m.Description = strings.Repeat("a", 1001) },
		"interval":          func(m *dtos.ChoreCatalogModel) { m.RecurringInterval = 366 },
		"malformed item id": func(m *dtos.ChoreCatalogModel) { m.Id = "SYSTEM" },
	}
	for name, mutate := range invalid {
		item := valid
		mutate(&item)
		if err := v.Validate(dtos.ChoreCatalogUpsertModel{Items: []dtos.ChoreCatalogModel{item}}); err == nil {
			t.Errorf("%s: invalid item accepted", name)
		}
	}

	tooMany := make([]dtos.ChoreCatalogModel, dtos.MaxChoreCatalogUpsertItems+1)
	for i := range tooMany {
		tooMany[i] = valid
	}
	if err := v.Validate(dtos.ChoreCatalogUpsertModel{Items: tooMany}); err == nil {
		t.Error("oversized upsert accepted")
	}
	if err := v.Validate(dtos.ChoreCatalogUpsertModel{}); err == nil {
		t.Error("empty upsert accepted")
	}
}

func TestChoreCategoryIsStoredAndKeptWhenAnUpdateOmitsIt(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	owner := fixture.SeedUser(t, "correct-password")
	house := fixture.CreateHouse(t, owner, 1)
	kitchen := entities.ChoreCategoryKitchen
	model := dtos.CreateChoreModel{
		Title: "Categorized chore", Description: "Category contract", AssignedTo: owner.Id.Hex(),
		DueDate: dtos.NewUTCDateTime(time.Now().Add(time.Hour)), HouseId: house.Id.Hex(), Level: entities.Easy,
		Category: &kitchen,
	}
	created, err := cqrs.Send[*dtos.ChoreResponseModel](fixture.Ctx, fixture.Sender, appkit.CreateChoreCommand(model, owner.Id.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	if created.Category != entities.ChoreCategoryKitchen {
		t.Fatalf("created category = %d, want kitchen", created.Category)
	}

	updateChore := func(category *entities.ChoreCategory) *dtos.ChoreResponseModel {
		t.Helper()
		updated, err := cqrs.Send[*dtos.ChoreResponseModel](fixture.Ctx, fixture.Sender, choreCommands.UpdateChoreCommand{
			ChoreID: created.Id, Title: "Categorized chore", Description: "Category contract",
			AssignedTo: owner.Id.Hex(), DueDate: model.DueDate.Time, HouseID: house.Id.Hex(), Level: entities.Easy,
			Category: category, RequesterID: owner.Id.Hex(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return updated
	}
	if updated := updateChore(nil); updated.Category != entities.ChoreCategoryKitchen {
		t.Fatalf("update without category changed it to %d", updated.Category)
	}
	pets := entities.ChoreCategoryPets
	if updated := updateChore(&pets); updated.Category != entities.ChoreCategoryPets {
		t.Fatalf("update category = %d, want pets", updated.Category)
	}

	v := validator.NewValidator()
	invalid := entities.ChoreCategory(10)
	model.Category = &invalid
	if err := v.Validate(model); err == nil {
		t.Fatal("invalid chore category accepted")
	}
	model.Category = nil
	if err := v.Validate(model); err != nil {
		t.Fatalf("chore without category rejected: %v", err)
	}
}

func TestSystemChoreCatalogSeedIsListedInTheUsersLanguage(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	user := fixture.SeedUser(t, "correct-password") // language "en"

	var system []entities.ChoreCatalog
	cursor, err := fixture.DB.Collection("ChoreCatalog").Find(fixture.Ctx, bson.M{"userId": entities.SystemUserId})
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.All(fixture.Ctx, &system); err != nil {
		t.Fatal(err)
	}
	if len(system) != 62 {
		t.Fatalf("seeded system entries = %d, want 62", len(system))
	}
	categories := map[entities.ChoreCategory]int{}
	for _, entry := range system {
		categories[entry.Category]++
		for _, key := range []string{entry.Title, entry.Description} {
			if !strings.HasPrefix(key, entities.SystemChoreCatalogKeyPrefix) {
				t.Fatalf("system entry %s stores %q, want a localization key", entry.Id.Hex(), key)
			}
			for _, language := range []string{"en", "tr"} {
				count, err := fixture.DB.Collection("localization").CountDocuments(fixture.Ctx, bson.M{
					"key": key, "language": language, "type": "message", "value": bson.M{"$ne": ""},
				})
				if err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("%s has %d %s translations, want 1", key, count, language)
				}
			}
		}
	}
	if len(categories) != 9 {
		t.Fatalf("seeded categories = %v, want all nine", categories)
	}

	personal := catalogItem("choreCatalog.system.kitchen.wash_dishes.title", entities.ChoreCategoryKitchen)
	mustUpsertCatalog(t, fixture, user.Id.Hex(), normalRole, personal)

	findTitle := func(items []dtos.ChoreCatalogModel, title string) *dtos.ChoreCatalogModel {
		for i := range items {
			if items[i].Title == title {
				return &items[i]
			}
		}
		return nil
	}
	english := listCatalog(t, fixture, user.Id.Hex())
	if len(english) != 63 {
		t.Fatalf("listed entries = %d, want 62 system + 1 personal", len(english))
	}
	washDishes := findTitle(english, "Wash the dishes")
	if washDishes == nil || !washDishes.IsSystem ||
		washDishes.Description != "Wash the dishes in the sink, rinse them and leave them to dry." {
		t.Fatalf("english system entry = %+v", washDishes)
	}
	// Personal text is never treated as a key, even when it looks like one.
	if entry := findTitle(english, personal.Title); entry == nil || entry.IsSystem {
		t.Fatalf("personal entry was localized: %+v", entry)
	}

	// The user's stored language wins over the token's.
	if _, err := fixture.DB.Collection("User").UpdateByID(fixture.Ctx, user.Id, bson.M{"$set": bson.M{"language": "tr"}}); err != nil {
		t.Fatal(err)
	}
	turkish := listCatalogWithTokenLanguage(t, fixture, user.Id.Hex(), "en")
	if entry := findTitle(turkish, "Bulaşıkları yıka"); entry == nil ||
		entry.Description != "Lavabodaki bulaşıkları yıka, durula ve kurumaya bırak." {
		t.Fatalf("turkish system entry missing: %v", catalogTitles(turkish))
	}

	// Without a stored language the token's language is used.
	if _, err := fixture.DB.Collection("User").UpdateByID(fixture.Ctx, user.Id, bson.M{"$set": bson.M{"language": ""}}); err != nil {
		t.Fatal(err)
	}
	if findTitle(listCatalogWithTokenLanguage(t, fixture, user.Id.Hex(), "tr"), "Bulaşıkları yıka") == nil {
		t.Fatal("token language was not used as the fallback")
	}
}
