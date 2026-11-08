package fausSwitch

import (
	"testing"

	"houseflowApi/tests/testkit/gamekit"
)

// The owner crash tests start this test in a child process.
func TestHouseRocketsOwnerProcessHelper(t *testing.T) {
	gamekit.RunOwnerProcessHelper(t)
}
