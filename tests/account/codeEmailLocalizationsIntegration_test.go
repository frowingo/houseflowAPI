package account

import (
	"reflect"
	"strings"
	"testing"

	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	infrastructureLocalization "houseflowApi/internal/infrastructure/localization"
	"houseflowApi/internal/services"
	"houseflowApi/tests/testkit/appkit"
)

// The migrated messages must say what the email service falls back to in
// English, and translate every text into Turkish.
func TestCodeEmailLocalizationsAreMigrated(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	cache := infrastructureLocalization.NewCache(
		database.NewDbContext[entities.Localization](fixture.DB.Client(), fixture.DB.Name()))
	if err := cache.Load(fixture.Ctx); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []services.CodeEmailKind{services.CodeEmailResetCode, services.CodeEmailVerificationCode} {
		for _, validity := range []int{1, 10} {
			fallback := services.LocalizeCodeEmail(fixture.Ctx, nil, kind, "en", "K7QM2X", validity)
			english := services.LocalizeCodeEmail(fixture.Ctx, cache, kind, "en", "K7QM2X", validity)
			if !reflect.DeepEqual(english, fallback) {
				t.Fatalf("%s/%d: migrated English %+v, want the fallback %+v", kind, validity, english, fallback)
			}

			turkish := services.LocalizeCodeEmail(fixture.Ctx, cache, kind, "tr", "K7QM2X", validity)
			if turkish.Language != "tr" {
				t.Fatalf("%s: language = %q", kind, turkish.Language)
			}
			for name, pair := range map[string][2]string{
				"Heading":       {turkish.Heading, english.Heading},
				"Intro":         {turkish.Intro, english.Intro},
				"ExpiresBefore": {turkish.ExpiresBefore, english.ExpiresBefore},
				"ExpiresValue":  {turkish.ExpiresValue, english.ExpiresValue},
				"IgnoreMessage": {turkish.IgnoreMessage, english.IgnoreMessage},
				"SecurityNote":  {turkish.SecurityNote, english.SecurityNote},
				"AutomatedNote": {turkish.AutomatedNote, english.AutomatedNote},
			} {
				if pair[0] == "" || pair[0] == pair[1] || strings.Contains(pair[0], "%s") {
					t.Fatalf("%s/%d: Turkish %s = %q (English %q)", kind, validity, name, pair[0], pair[1])
				}
			}
		}
	}
}
