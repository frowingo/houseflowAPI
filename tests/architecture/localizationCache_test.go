package architecture

import (
	"context"
	"sync"
	"testing"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	infrastructureLocalization "houseflowApi/internal/infrastructure/localization"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// localizationStore answers FindManyByFilter from memory and counts the calls.
type localizationStore struct {
	databaseAbstract.DbRepository[entities.Localization]
	mu      sync.Mutex
	items   []entities.Localization
	queries int
}

func (s *localizationStore) FindManyByFilter(_ context.Context, filter bson.M, _ ...*options.FindOptions) ([]entities.Localization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	var result []entities.Localization
	for _, item := range s.items {
		if item.Type != filter["type"] {
			continue
		}
		if key, ok := filter["key"]; ok && item.Key != key {
			continue
		}
		switch language := filter["language"].(type) {
		case bson.M:
			matched := false
			for _, candidate := range language["$in"].([]string) {
				matched = matched || string(item.Language) == candidate
			}
			if !matched {
				continue
			}
		default:
			if item.Language != language {
				continue
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *localizationStore) queryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries
}

func TestLocalizationCacheQueriesAMissingTranslationOnce(t *testing.T) {
	store := &localizationStore{items: []entities.Localization{
		{Type: entities.Message, Language: entities.English, Key: "auth.error.invalid_token", Value: "Invalid token"},
		{Type: entities.Message, Language: entities.Turkish, Key: "auth.error.invalid_token", Value: "Geçersiz token"},
		{Type: entities.Message, Language: entities.English, Key: "rate_limit.error.too_many_requests", Value: "Too many requests"},
	}}
	cache := infrastructureLocalization.NewCache(store)
	ctx := context.Background()
	if err := cache.Load(ctx); err != nil {
		t.Fatal(err)
	}
	afterLoad := store.queryCount()

	if got := cache.LocalizeMessage(ctx, "tr-TR", "auth.error.invalid_token"); got != "Geçersiz token" {
		t.Fatalf("warmed Turkish message=%q", got)
	}
	// Unsupported languages fall back to English without a query.
	if got := cache.LocalizeMessage(ctx, "de-DE", "auth.error.invalid_token"); got != "Invalid token" {
		t.Fatalf("unsupported language message=%q", got)
	}
	if store.queryCount() != afterLoad {
		t.Fatalf("queries=%d after cached lookups, want %d", store.queryCount(), afterLoad)
	}

	// A missing Turkish translation is queried once, then served from English.
	for i := 0; i < 3; i++ {
		if got := cache.LocalizeMessage(ctx, "tr", "rate_limit.error.too_many_requests"); got != "Too many requests" {
			t.Fatalf("missing Turkish message=%q", got)
		}
	}
	if store.queryCount() != afterLoad+1 {
		t.Fatalf("queries=%d, want one query for the missing translation", store.queryCount()-afterLoad)
	}

	// A later write replaces the cached miss.
	cache.SetLocalization(entities.Localization{
		Type: entities.Message, Language: entities.Turkish, Key: "rate_limit.error.too_many_requests", Value: "Çok fazla istek",
	})
	if got := cache.LocalizeMessage(ctx, "tr", "rate_limit.error.too_many_requests"); got != "Çok fazla istek" {
		t.Fatalf("message after write=%q", got)
	}
}

func TestLocalizationCacheFormatsMessageArguments(t *testing.T) {
	store := &localizationStore{items: []entities.Localization{
		{Type: entities.Message, Language: entities.English, Key: "notification.chore_reminder.title", Value: "Reminder: {1}"},
		{Type: entities.Message, Language: entities.Turkish, Key: "notification.chore_reminder.message", Value: "{0}, \"{1}\" işini süresi dolmadan yapmanı hatırlatıyor."},
		{Type: entities.Message, Language: entities.English, Key: "email.code.minutes", Value: "%s minutes"},
		{Type: entities.Message, Language: entities.English, Key: "user.error.invalid_field", Value: "Invalid field"},
		{Type: entities.Message, Language: entities.English, Key: "user.error.invalid_fields", Value: "Invalid fields:"},
		{Type: entities.Message, Language: entities.English, Key: "user.error.detail", Value: "Check {detail} again"},
	}}
	cache := infrastructureLocalization.NewCache(store)
	ctx := context.Background()
	if err := cache.Load(ctx); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct{ language, message, want string }{
		// Positional placeholders place the arguments; nothing is appended.
		{"en", "notification.chore_reminder.title||Ayşe||Dishes", "Reminder: Dishes"},
		{"tr", "notification.chore_reminder.message||Ayşe||Bulaşık", "Ayşe, \"Bulaşık\" işini süresi dolmadan yapmanı hatırlatıyor."},
		// A placeholder inside an argument is user text and stays as typed.
		{"en", "notification.chore_reminder.title||{1}||Fix {0} and {1}", "Reminder: Fix {0} and {1}"},
		{"en", "email.code.minutes||10", "10 minutes"},
		// Messages without placeholders keep getting their arguments appended.
		{"en", "user.error.invalid_field||email", "Invalid field: email"},
		{"en", "user.error.invalid_fields||email||name", "Invalid fields: email, name"},
		{"en", "user.error.detail||email||name", "Check email, name again"},
	} {
		if got := cache.LocalizeMessage(ctx, test.language, test.message); got != test.want {
			t.Errorf("LocalizeMessage(%q, %q) = %q, want %q", test.language, test.message, got, test.want)
		}
	}
}
