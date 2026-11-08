// Package testkit holds what every test package under tests/ shares: TestMain,
// the pooled migrated Mongo databases, Redis selection and repository paths.
// It imports no application code: the application fixture lives in
// testkit/appkit and the game session, room runtime and realtime gateway
// fixtures in testkit/gamekit, so scripts/testAffected.sh runs a test package
// only for changes to code it uses. Apart from the pool's guard test it has
// no tests of its own.
package testkit

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
)

const (
	redisURLVariable = "HOUSEFLOW_TEST_REDIS_URL"
	// The value HOUSEFLOW_TEST_REDIS_URL had before Main changed it. Child
	// processes started by a test inherit both, so they select the same
	// database instead of adding the slot twice.
	redisBaseURLVariable = "HOUSEFLOW_TEST_REDIS_BASE_URL"
)

// go test runs each package in its own process, several at a time. Mongo
// needs nothing: every process migrates databases under unique names. Redis
// tests flush their database, so each package gets its own:
// HOUSEFLOW_TEST_REDIS_URL names the first test database N, and a package
// uses N + its slot. The Redis server needs N + len(redisSlots) databases
// (16 by default, so N <= 5); the database the app uses must lie outside.
var redisSlots = map[string]int{
	"testkit":      0,
	"architecture": 1,
	"account":      2,
	"house":        3,
	"chore":        4,
	"gamecore":     5,
	"realtime":     6,
	"houseRockets": 7,
	"houseTanks":   8,
	"fausSwitch":   9,
	"ticTacToe":    10,
}

// Main is every test package's TestMain: it selects the package's Redis
// database, runs the tests and drops the package's pooled Mongo databases.
func Main(m *testing.M, packageName string) {
	if err := selectRedisDatabase(packageName); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	testDatabases.close()
	os.Exit(code)
}

func selectRedisDatabase(packageName string) error {
	slot, ok := redisSlots[packageName]
	if !ok {
		return fmt.Errorf("testkit: package %q has no Redis slot; add it to redisSlots", packageName)
	}
	base := os.Getenv(redisBaseURLVariable)
	if base == "" {
		base = os.Getenv(redisURLVariable)
		if base == "" {
			return nil // Redis tests skip.
		}
		if err := os.Setenv(redisBaseURLVariable, base); err != nil {
			return err
		}
	}
	options, err := redis.ParseURL(base)
	if err != nil {
		return fmt.Errorf("testkit: %s: %w", redisURLVariable, err)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("testkit: %s: %w", redisURLVariable, err)
	}
	parsed.Path = "/" + strconv.Itoa(options.DB+slot)
	return os.Setenv(redisURLVariable, parsed.String())
}
