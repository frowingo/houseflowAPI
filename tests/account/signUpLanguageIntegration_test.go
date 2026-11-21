package account

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/tests/testkit/appkit"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

// Push texts use the account language, so an email signup stores the
// language of the request instead of always English.
func TestSignUpStoresTheRequestLanguage(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	app := fiber.New()
	app.Post("/auth/signup", controllers.NewAuthController(fixture.Sender).Signup)

	cases := []struct {
		name, acceptLanguage, want string
	}{
		{"turkish device", "tr-TR,tr;q=0.9,en;q=0.8", "tr"},
		{"english device", "en-GB", "en"},
		{"no header", "", "en"},
		{"unsupported language", "de-DE,de;q=0.9", "en"},
	}
	for index, testCase := range cases {
		email := "signup-language-" + string(rune('a'+index)) + "@example.test"
		body := `{"email":"` + email + `","password":"signup-pass","firstName":"Dil","lastName":"Test"}`
		request := httptest.NewRequest(fiber.MethodPost, "/auth/signup", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if testCase.acceptLanguage != "" {
			request.Header.Set("Accept-Language", testCase.acceptLanguage)
		}
		response, err := app.Test(request, int((10 * time.Second).Milliseconds()))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusCreated {
			t.Fatalf("%s: status = %d", testCase.name, response.StatusCode)
		}

		var user entities.User
		if err := fixture.DB.Collection("User").FindOne(fixture.Ctx, bson.M{"email": email}).Decode(&user); err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		if user.Language != testCase.want {
			t.Errorf("%s: language = %q, want %q", testCase.name, user.Language, testCase.want)
		}
	}
}
