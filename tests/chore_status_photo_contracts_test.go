package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"testing"
	"time"

	choreCommands "houseflowApi/internal/application/chore/commands"
	choreQueries "houseflowApi/internal/application/chore/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
)

type choreStatusCaptureSender struct {
	command choreCommands.UpdateChoreStatusCommand
	called  bool
}

func (s *choreStatusCaptureSender) Send(_ context.Context, request any) (any, error) {
	s.command = request.(choreCommands.UpdateChoreStatusCommand)
	s.called = true
	if s.command.Photo != nil {
		_, _ = io.ReadAll(s.command.Photo.Reader)
	}
	return []dtos.ChoreResponseModel{}, nil
}

func choreStatusTestApp(sender *choreStatusCaptureSender) *fiber.App {
	app := fiber.New()
	controller := controllers.NewChoreController(sender, nil)
	app.Put("/chore/status", func(c *fiber.Ctx) error {
		c.Locals("userID", "507f1f77bcf86cd799439012")
		return c.Next()
	}, controller.UpdateChoreStatus)
	return app
}

func TestUpdateChoreStatusKeepsJSONContract(t *testing.T) {
	sender := &choreStatusCaptureSender{}
	payload := `{"houseId":"507f1f77bcf86cd799439011","chores":[{"choreId":"507f1f77bcf86cd799439013","status":1}]}`
	request := httptest.NewRequest(fiber.MethodPut, "/chore/status", bytes.NewBufferString(payload))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	response, err := choreStatusTestApp(sender).Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusOK || !sender.called {
		t.Fatalf("status=%d sender called=%v", response.StatusCode, sender.called)
	}
	if sender.command.Photo != nil || len(sender.command.Chores) != 1 || sender.command.Chores[0].Status != entities.Progress {
		t.Fatalf("unexpected JSON command: %+v", sender.command)
	}
}

func TestUpdateChoreStatusAcceptsSingleMultipartReviewTransition(t *testing.T) {
	sender := &choreStatusCaptureSender{}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("houseId", "507f1f77bcf86cd799439011")
	chores, _ := json.Marshal([]dtos.UpdateChoreStatusModel{{
		ChoreId: "507f1f77bcf86cd799439013",
		Status:  entities.InTest,
	}})
	_ = writer.WriteField("chores", string(chores))
	file, err := writer.CreateFormFile("file", "proof.jpg")
	if err != nil {
		t.Fatal(err)
	}
	photo := []byte{0xff, 0xd8, 0xff, 0xe0, 0x01}
	_, _ = file.Write(photo)
	_ = writer.Close()

	request := httptest.NewRequest(fiber.MethodPut, "/chore/status", &body)
	request.Header.Set(fiber.HeaderContentType, writer.FormDataContentType())
	response, err := choreStatusTestApp(sender).Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusOK || !sender.called {
		t.Fatalf("status=%d sender called=%v", response.StatusCode, sender.called)
	}
	if sender.command.Photo == nil || sender.command.Photo.Size != int64(len(photo)) {
		t.Fatalf("photo command=%+v", sender.command.Photo)
	}
	if len(sender.command.Chores) != 1 || sender.command.Chores[0].Status != entities.InTest {
		t.Fatalf("unexpected multipart command: %+v", sender.command)
	}
}

func TestUpdateChoreStatusRejectsMultipartBulkTransition(t *testing.T) {
	sender := &choreStatusCaptureSender{}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("houseId", "507f1f77bcf86cd799439011")
	chores, _ := json.Marshal([]dtos.UpdateChoreStatusModel{
		{ChoreId: "507f1f77bcf86cd799439013", Status: entities.InTest},
		{ChoreId: "507f1f77bcf86cd799439014", Status: entities.InTest},
	})
	_ = writer.WriteField("chores", string(chores))
	_ = writer.Close()

	request := httptest.NewRequest(fiber.MethodPut, "/chore/status", &body)
	request.Header.Set(fiber.HeaderContentType, writer.FormDataContentType())
	response, err := choreStatusTestApp(sender).Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusBadRequest || sender.called {
		t.Fatalf("status=%d sender called=%v", response.StatusCode, sender.called)
	}
}

type reviewPhotoURLCaptureSender struct {
	query  choreQueries.GetReviewPhotoURLQuery
	called bool
}

func (s *reviewPhotoURLCaptureSender) Send(_ context.Context, request any) (any, error) {
	s.query = request.(choreQueries.GetReviewPhotoURLQuery)
	s.called = true
	return &dtos.ChoreReviewPhotoURLModel{
		URL:       "https://private-storage.test/signed",
		ExpiresAt: dtos.NewUTCDateTime(time.Now().Add(5 * time.Minute)),
	}, nil
}

func reviewPhotoURLTestApp(sender *reviewPhotoURLCaptureSender) *fiber.App {
	app := fiber.New()
	controller := controllers.NewChoreController(sender, nil)
	app.Get("/chore/:id/review-photo/:round", func(c *fiber.Ctx) error {
		c.Locals("userID", "507f1f77bcf86cd799439012")
		return c.Next()
	}, controller.GetReviewPhotoURL)
	return app
}

func TestGetReviewPhotoURLBuildsAuthorizedQuery(t *testing.T) {
	sender := &reviewPhotoURLCaptureSender{}
	request := httptest.NewRequest(fiber.MethodGet,
		"/chore/507f1f77bcf86cd799439013/review-photo/2", nil)
	response, err := reviewPhotoURLTestApp(sender).Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusOK || !sender.called {
		t.Fatalf("status=%d sender called=%v", response.StatusCode, sender.called)
	}
	if response.Header.Get(fiber.HeaderCacheControl) != "private, no-store" {
		t.Fatalf("cache-control=%q", response.Header.Get(fiber.HeaderCacheControl))
	}
	if sender.query.ChoreID != "507f1f77bcf86cd799439013" || sender.query.Round != 2 ||
		sender.query.RequesterID != "507f1f77bcf86cd799439012" {
		t.Fatalf("unexpected query: %+v", sender.query)
	}
	var result dtos.ChoreReviewPhotoURLModel
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://private-storage.test/signed" || result.ExpiresAt.IsZero() {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestGetReviewPhotoURLRejectsInvalidRoundBeforeDispatch(t *testing.T) {
	sender := &reviewPhotoURLCaptureSender{}
	request := httptest.NewRequest(fiber.MethodGet,
		"/chore/507f1f77bcf86cd799439013/review-photo/not-a-round", nil)
	response, err := reviewPhotoURLTestApp(sender).Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusBadRequest || sender.called {
		t.Fatalf("status=%d sender called=%v", response.StatusCode, sender.called)
	}
}
