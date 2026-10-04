package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type RealtimeLimits struct {
	MaxOwnedRooms  int
	MaxConnections int
}

// Enabling House Rockets requires explicit per-instance operational limits.
func LoadRealtimeLimits() (RealtimeLimits, error) {
	var result RealtimeLimits
	for _, setting := range []struct {
		name  string
		value *int
	}{{"REALTIME_MAX_OWNED_ROOMS", &result.MaxOwnedRooms}, {"REALTIME_MAX_CONNECTIONS", &result.MaxConnections}} {
		raw := strings.TrimSpace(os.Getenv(setting.name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return result, fmt.Errorf("%s must be a positive integer", setting.name)
		}
		*setting.value = value
	}
	if HouseRocketsEnabled() && (result.MaxOwnedRooms == 0 || result.MaxConnections == 0) {
		return result, fmt.Errorf("enabled House Rockets requires REALTIME_MAX_OWNED_ROOMS and REALTIME_MAX_CONNECTIONS")
	}
	return result, nil
}

// HouseRocketsEnabled is controlled independently of the application's environment.
func HouseRocketsEnabled() bool {
	return os.Getenv("HOUSE_ROCKETS_ENABLED") == "true"
}
