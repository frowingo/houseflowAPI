package config

import (
	"os"
	"strings"
)

// HouseRocketsEnabled restricts the initial online rollout to test environments.
// Production requires the result/recovery and release acceptance packages.
func HouseRocketsEnabled() bool {
	if os.Getenv("HOUSE_ROCKETS_ENABLED") != "true" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "local", "development", "staging":
		return true
	default:
		return false
	}
}
