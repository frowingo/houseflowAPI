package fausSwitch

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The Swift parity trace: seeded flip races run through the Swift reference
// engine (testdata/swiftParity/main.swift, built against a copy of the
// FausSwitch engine sources) and through this port must print the same text,
// every float as its bit pattern. The digests of the Swift run are checked in;
// FAUS_SWITCH_TRACE=<file> writes the Go trace in full for a diff.

// parityRandom is the shared 64-bit LCG of the trace.
type parityRandom uint64

func (state *parityRandom) next() uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return uint64(*state) >> 33
}

var flipOutcomeNames = map[flipOutcome]string{flipped: "flipped", flipUnsupported: "unsupported", flipNotRunning: "notRunning"}

func parityIDs(set padSet) string {
	var text []string
	for _, id := range set.ids() {
		text = append(text, strconv.Itoa(id))
	}
	return strings.Join(text, ",")
}

func parityTick(value int64, set bool) int64 {
	if !set {
		return -1
	}
	return value
}

// writeParityTrace writes every scenario's trace to out and returns each
// scenario's sha256, keyed by its header line.
func writeParityTrace(out io.Writer) map[string]string {
	digests := map[string]string{}
	for count := 1; count <= MaximumPlayers; count++ {
		for seed := uint64(1); seed <= 3; seed++ {
			for _, every := range []uint64{25, 60} {
				rules := newRacePhysics(count)
				runners := startRunners(count)
				random := parityRandom(seed*1000 + uint64(count))
				header := fmt.Sprintf("scenario %d %d %d", count, seed, every)
				var digest hash.Hash = sha256.New()
				line := func(format string, values ...any) {
					text := fmt.Sprintf(format, values...)
					digest.Write([]byte(text))
					io.WriteString(out, text)
				}
				line("%s\n", header)
				for tick := int64(0); tick < MaximumMatchTicks && anyRunning(runners); {
					for seat := range runners {
						if random.next()%every == 0 {
							line("flip %d %d %s\n", tick, seat, flipOutcomeNames[rules.flip(runners, seat)])
						}
					}
					rules.step(runners, tick)
					tick++
					for seat := range runners {
						robot := &runners[seat]
						reason := "none"
						if robot.crashReason != 0 {
							reason = robot.crashReason.String()
						}
						line("%d %d %x %x %x %v %v %d %d %s %d %d [%s] [%s]\n", tick, seat,
							math.Float64bits(robot.x), math.Float64bits(robot.y), math.Float64bits(robot.velocityY), robot.gravity, robot.status,
							parityTick(robot.finishedAtTick, robot.status == RunnerFinished),
							parityTick(robot.crashedAtTick, robot.status == RunnerCrashed), reason,
							robot.boostedUntilTick, robot.slowedUntilTick, parityIDs(robot.usedBoosts), parityIDs(robot.usedSlows))
					}
				}
				digests[header] = hex.EncodeToString(digest.Sum(nil))
			}
		}
	}
	return digests
}

func TestSwiftParityTrace(t *testing.T) {
	out := io.Discard
	if path := os.Getenv("FAUS_SWITCH_TRACE"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		buffered := bufio.NewWriter(file)
		defer func() { buffered.Flush(); file.Close() }()
		out = buffered
	}
	got := writeParityTrace(out)
	want := readParityDigests(t)
	if len(got) != len(want) {
		t.Fatalf("%d scenarios, want %d", len(got), len(want))
	}
	for header, digest := range want {
		if got[header] != digest {
			t.Errorf("%s: trace differs from the Swift engine", header)
		}
	}
}

func readParityDigests(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/swiftParity/digests.txt")
	if err != nil {
		t.Fatal(err)
	}
	digests := map[string]string{}
	for _, row := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		header, digest, ok := strings.Cut(row, "\t")
		if !ok {
			t.Fatalf("bad digest row %q", row)
		}
		digests[header] = digest
	}
	return digests
}
