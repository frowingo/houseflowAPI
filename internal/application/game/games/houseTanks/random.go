package houseTanks

import (
	"fmt"
	"hash/fnv"
)

// Random is the seedable SplitMix64 generator of the Swift reference engine.
// Online the server draws every random choice and sends the outcome, so
// clients never need to reproduce the sequence.
type Random struct {
	State uint64 `json:"state"`
}

func NewRandom(seed uint64) Random { return Random{State: seed} }

func (random *Random) Next() uint64 {
	random.State += 0x9E3779B97F4A7C15
	value := random.State
	value = (value ^ (value >> 30)) * 0xBF58476D1CE4E5B9
	value = (value ^ (value >> 27)) * 0x94D049BB133111EB
	return value ^ (value >> 31)
}

// Unit is a uniform value in [0, 1).
func (random *Random) Unit() float64 {
	return float64(random.Next()>>11) / float64(uint64(1)<<53)
}

// Between is a uniform value in [lower, upper).
func (random *Random) Between(lower, upper float64) float64 {
	return lower + random.Unit()*(upper-lower)
}

// Below is a uniform integer in [0, count).
func (random *Random) Below(count int) int {
	return int(random.Next() % uint64(count))
}

// MatchSeed derives a match's seed from its session, so every owner that
// recovers the match draws the same map and jokers.
func MatchSeed(sessionID string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(sessionID))
	return hash.Sum64()
}

// SeedText is the wire form of a seed; a JSON number would lose precision.
func SeedText(seed uint64) string { return fmt.Sprintf("%016x", seed) }
