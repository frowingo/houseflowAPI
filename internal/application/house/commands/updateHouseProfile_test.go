package commands

import (
	"testing"

	"houseflowApi/internal/data/entities"
)

func TestReviewPhotoSettingUsesBooleanHouseFieldAndValidatorCompatibleAuditValue(t *testing.T) {
	enabled := true
	changes, fields, err := houseProfileChanges(UpdateHouseProfileCommand{
		IsChoreReviewPhotoRequired: &enabled,
	}, &entities.House{IsChoreReviewPhotoRequired: false})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := fields["isChoreReviewPhotoRequired"].(bool); !ok || !got {
		t.Fatalf("house field=%#v, want boolean true", fields["isChoreReviewPhotoRequired"])
	}
	if len(changes) != 1 || changes[0].value != "true" {
		t.Fatalf("changes=%+v, want validator-compatible audit string", changes)
	}
}
