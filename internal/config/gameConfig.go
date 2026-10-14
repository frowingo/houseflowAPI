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

// Which games run is stored in the Game collection; per-instance capacity is
// infrastructure. Production must state it explicitly; elsewhere an unset
// limit means unlimited.
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
	if !isDebugMode() && (result.MaxOwnedRooms == 0 || result.MaxConnections == 0) {
		return result, fmt.Errorf("production realtime requires REALTIME_MAX_OWNED_ROOMS and REALTIME_MAX_CONNECTIONS")
	}
	return result, nil
}
