package commands

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"houseflowApi/internal/helpers"
)

func TestPrepareChoreReviewPhotoDetectsContentAndPreservesReader(t *testing.T) {
	payload := append([]byte{0xff, 0xd8, 0xff, 0xe0}, []byte("review-photo")...)
	reader, contentType, extension, err := prepareChoreReviewPhoto(&ChoreReviewPhoto{
		Reader: bytes.NewReader(payload),
		Size:   int64(len(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/jpeg" || extension != ".jpg" {
		t.Fatalf("content type=%q extension=%q", contentType, extension)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("prepared reader changed payload: %x", got)
	}
}

func TestPrepareChoreReviewPhotoRejectsOversizedAndSpoofedFiles(t *testing.T) {
	_, _, _, err := prepareChoreReviewPhoto(&ChoreReviewPhoto{
		Reader: bytes.NewReader([]byte("small")),
		Size:   maxChoreReviewPhotoSize + 1,
	})
	if !helpers.IsApplicationError(err, "chore.error.review_photo_too_large") {
		t.Fatalf("oversized photo error=%v", err)
	}

	_, _, _, err = prepareChoreReviewPhoto(&ChoreReviewPhoto{
		Reader: strings.NewReader("not an image"),
		Size:   int64(len("not an image")),
	})
	if !helpers.IsApplicationError(err, "chore.error.review_photo_type_unsupported") {
		t.Fatalf("spoofed photo error=%v", err)
	}
}
