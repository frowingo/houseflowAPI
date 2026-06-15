package dtos

import (
	"reflect"
	"testing"
)

func TestUpdateUserModelDoesNotExposeVerificationFlags(t *testing.T) {
	modelType := reflect.TypeOf(UpdateUserModel{})

	if _, ok := modelType.FieldByName("IsVerifyPhone"); ok {
		t.Fatal("UpdateUserModel must not allow clients to update phone verification")
	}
	if _, ok := modelType.FieldByName("IsVerifyEmail"); ok {
		t.Fatal("UpdateUserModel must not allow clients to update email verification")
	}
}
