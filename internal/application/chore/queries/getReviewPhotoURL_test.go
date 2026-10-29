package queries

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type fakeReviewPhotoChoreReader struct {
	chore *entities.Chore
	err   error
}

func (f *fakeReviewPhotoChoreReader) FindByID(context.Context, primitive.ObjectID) (*entities.Chore, error) {
	return f.chore, f.err
}

type fakeReviewPhotoMembershipPolicy struct {
	house   *entities.House
	err     error
	houseID string
	userID  string
}

func (f *fakeReviewPhotoMembershipPolicy) RequireMember(_ context.Context, houseID string, userID string) (*entities.House, error) {
	f.houseID = houseID
	f.userID = userID
	return f.house, f.err
}

type fakeReviewPhotoStorage struct {
	url       string
	err       error
	objectKey string
	ttl       time.Duration
}

func (f *fakeReviewPhotoStorage) PutImage(context.Context, string, io.Reader, int64, string) error {
	return nil
}

func (f *fakeReviewPhotoStorage) DeleteObject(context.Context, string) error { return nil }

func (f *fakeReviewPhotoStorage) PresignGet(_ context.Context, objectKey string, ttl time.Duration) (string, error) {
	f.objectKey = objectKey
	f.ttl = ttl
	return f.url, f.err
}

func TestGetReviewPhotoURLChecksMembershipAndPresignsForFiveMinutes(t *testing.T) {
	choreID := primitive.NewObjectID()
	houseID := primitive.NewObjectID().Hex()
	reader := &fakeReviewPhotoChoreReader{chore: &entities.Chore{
		Id: choreID, HouseId: houseID, ReviewPhotoKeys: map[string]string{"2": "private/round-2.jpg"},
	}}
	membership := &fakeReviewPhotoMembershipPolicy{house: &entities.House{Id: primitive.NewObjectID()}}
	storage := &fakeReviewPhotoStorage{url: "https://storage.test/signed"}
	handler := NewGetReviewPhotoURLHandler(reader, membership, storage)
	startedAt := time.Now()

	result, err := handler.Handle(context.Background(), GetReviewPhotoURLQuery{
		ChoreID: choreID.Hex(), Round: 2, RequesterID: "member-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if membership.houseID != houseID || membership.userID != "member-id" {
		t.Fatalf("membership check house=%q user=%q", membership.houseID, membership.userID)
	}
	if storage.objectKey != "private/round-2.jpg" || storage.ttl != 5*time.Minute {
		t.Fatalf("presign key=%q ttl=%s", storage.objectKey, storage.ttl)
	}
	if result.URL != storage.url || result.ExpiresAt.Before(startedAt.Add(5*time.Minute-time.Second)) {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGetReviewPhotoURLDoesNotPresignForNonMember(t *testing.T) {
	denied := helpers.NewForbiddenError("house.error.user_not_member")
	reader := &fakeReviewPhotoChoreReader{chore: &entities.Chore{
		Id: primitive.NewObjectID(), HouseId: primitive.NewObjectID().Hex(),
		ReviewPhotoKeys: map[string]string{"1": "private/round-1.jpg"},
	}}
	membership := &fakeReviewPhotoMembershipPolicy{err: denied}
	storage := &fakeReviewPhotoStorage{url: "https://storage.test/signed"}
	handler := NewGetReviewPhotoURLHandler(reader, membership, storage)

	_, err := handler.Handle(context.Background(), GetReviewPhotoURLQuery{
		ChoreID: reader.chore.Id.Hex(), Round: 1, RequesterID: "outsider",
	})
	if !errors.Is(err, denied) {
		t.Fatalf("error=%v, want membership denial", err)
	}
	if storage.objectKey != "" {
		t.Fatalf("storage called for denied member with key %q", storage.objectKey)
	}
}

func TestGetReviewPhotoURLRejectsMissingRoundPhoto(t *testing.T) {
	reader := &fakeReviewPhotoChoreReader{chore: &entities.Chore{
		Id: primitive.NewObjectID(), HouseId: primitive.NewObjectID().Hex(), ReviewPhotoKeys: map[string]string{},
	}}
	membership := &fakeReviewPhotoMembershipPolicy{house: &entities.House{}}
	storage := &fakeReviewPhotoStorage{}
	handler := NewGetReviewPhotoURLHandler(reader, membership, storage)

	_, err := handler.Handle(context.Background(), GetReviewPhotoURLQuery{
		ChoreID: reader.chore.Id.Hex(), Round: 3, RequesterID: "member",
	})
	if !helpers.IsApplicationError(err, "chore.error.review_photo_not_found") {
		t.Fatalf("missing photo error=%v", err)
	}
}

func TestGetReviewPhotoURLWrapsPresignFailureAsUnavailable(t *testing.T) {
	providerError := errors.New("presign failed")
	reader := &fakeReviewPhotoChoreReader{chore: &entities.Chore{
		Id: primitive.NewObjectID(), HouseId: primitive.NewObjectID().Hex(),
		ReviewPhotoKeys: map[string]string{"1": "private/round-1.jpg"},
	}}
	membership := &fakeReviewPhotoMembershipPolicy{house: &entities.House{}}
	storage := &fakeReviewPhotoStorage{err: providerError}
	handler := NewGetReviewPhotoURLHandler(reader, membership, storage)

	_, err := handler.Handle(context.Background(), GetReviewPhotoURLQuery{
		ChoreID: reader.chore.Id.Hex(), Round: 1, RequesterID: "member",
	})
	if !helpers.IsApplicationError(err, "chore.error.review_photo_url_failed") || !errors.Is(err, providerError) {
		t.Fatalf("presign error=%v", err)
	}
}

func TestGetReviewPhotoURLMapsMissingChoreToChoreNotFound(t *testing.T) {
	reader := &fakeReviewPhotoChoreReader{err: helpers.NewNotFoundError("database.error.document_not_found")}
	handler := NewGetReviewPhotoURLHandler(reader, &fakeReviewPhotoMembershipPolicy{}, &fakeReviewPhotoStorage{})

	_, err := handler.Handle(context.Background(), GetReviewPhotoURLQuery{
		ChoreID: primitive.NewObjectID().Hex(), Round: 1, RequesterID: "member",
	})
	if !helpers.IsApplicationError(err, "chore.error.not_found") {
		t.Fatalf("missing chore error=%v", err)
	}
}
