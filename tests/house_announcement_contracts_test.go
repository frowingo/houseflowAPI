package tests

import (
	"encoding/json"
	"testing"
	"time"

	"houseflowApi/external/validator"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestAnnouncementRequestRequiresHouseID(t *testing.T) {
	model := dtos.CreateAnnouncementModel{
		Title:       "Water outage",
		Description: "Water will be unavailable.",
	}

	if err := validator.NewValidator().Validate(model); err == nil {
		t.Fatal("announcement request without houseId must be invalid")
	}
}

func TestAnnouncementEntityUsesRequestHouseAnd24HourWindow(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 30, 0, 0, time.UTC)
	houseId := primitive.NewObjectID().Hex()
	userId := primitive.NewObjectID().Hex()
	model := dtos.CreateAnnouncementModel{
		HouseId:     houseId,
		Title:       "Water outage",
		Description: "Water will be unavailable.",
	}

	if err := validator.NewValidator().Validate(model); err != nil {
		t.Fatalf("valid announcement request was rejected: %v", err)
	}

	entity := model.ToEntity(userId, now)
	if entity.HouseId != houseId || entity.UserId != userId {
		t.Fatalf("unexpected announcement ownership: %+v", entity)
	}
	if !entity.CreatedOn.Equal(now) {
		t.Fatalf("CreatedOn = %v, want %v", entity.CreatedOn, now)
	}
	if want := now.Add(24 * time.Hour); !entity.DisplayUntil.Equal(want) {
		t.Fatalf("DisplayUntil = %v, want %v", entity.DisplayUntil, want)
	}
}

func TestAnnouncementResponseIncludesAnnouncerName(t *testing.T) {
	announcement := entities.Announcement{
		Id:          primitive.NewObjectID(),
		HouseId:     primitive.NewObjectID().Hex(),
		Title:       "Water outage",
		Description: "Water will be unavailable.",
		CreatedOn:   time.Now(),
	}

	response := dtos.AnnouncementToResponseModel(announcement, "Ada Lovelace")
	if response.AnnouncedBy != "Ada Lovelace" {
		t.Fatalf("announcedBy = %q, want %q", response.AnnouncedBy, "Ada Lovelace")
	}
	if response.HouseId != announcement.HouseId {
		t.Fatalf("houseId = %q, want %q", response.HouseId, announcement.HouseId)
	}

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}

	wantKeys := []string{"id", "title", "description", "houseId", "announcedBy", "createdOn"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("response has %d fields, want %d: %s", len(fields), len(wantKeys), payload)
	}
	for _, key := range wantKeys {
		if _, ok := fields[key]; !ok {
			t.Errorf("response is missing %q: %s", key, payload)
		}
	}
}
