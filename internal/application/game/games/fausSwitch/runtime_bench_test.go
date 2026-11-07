package fausSwitch

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"math/rand/v2"
	"os"
	goruntime "runtime"
	"slices"
	"testing"
	"time"

	"houseflowApi/internal/application/game/module"
)

// schedulerStep is one 60 Hz scheduler step, rounded up so three steps make
// the 50 ms a player needs between flips at the 20 Hz rate limit.
const schedulerStep = time.Second/SchedulingRateHz + 1

// loadedRace is a full room under the heaviest input the protocol admits:
// every robot flips at the rate limit, each flip seen 0–30 ticks late, so
// nearly every scheduler step rewinds. A race is replaced by a fresh one as
// soon as one robot stops, so every step moves eight running robots.
type loadedRace struct {
	tb        testing.TB
	random    *rand.Rand
	driver    *raceDriver
	players   []string
	sequences []int64
	steps     int
	ticks     int64
	flips     int
	refused   int
	races     int
	// onFrame sees every published frame; nil drops them.
	onFrame func(RuntimeFrame)
}

func newLoadedRace(tb testing.TB, count int) *loadedRace {
	load := &loadedRace{tb: tb, random: rand.New(rand.NewPCG(7, 7)), sequences: make([]int64, count), players: make([]string, count)}
	for seat := range load.players {
		load.players[seat] = playerID(seat)
	}
	load.restart()
	return load
}

func (load *loadedRace) restart() {
	load.driver = newDriver(load.tb, len(load.sequences))
	load.driver.bindAll()
	clear(load.sequences)
	load.races++
}

func (load *loadedRace) running() bool {
	for seat := range load.driver.runtime.race.world {
		if !load.driver.runtime.race.world[seat].running() {
			return false
		}
	}
	return true
}

// step submits this step's flips and heartbeats, advances the runtime one
// scheduler step and drains its mailboxes, as the room does.
func (load *loadedRace) step() {
	driver, runtime := load.driver, load.driver.runtime
	tick := runtime.race.tick
	for seat, id := range load.players {
		if (load.steps+seat)%3 == 0 {
			load.sequences[seat]++
			input := driver.input(id, InputFlip)
			input.InputSequence, input.ClientTick = load.sequences[seat], max(0, tick-load.random.Int64N(MaximumRewindTicks+1))
			if runtime.Submit(input, driver.now) == nil {
				load.flips++
			} else {
				load.refused++
			}
		}
		if load.steps%SchedulingRateHz == seat {
			_ = runtime.Submit(driver.input(id, InputHeartbeat), driver.now)
		}
	}
	driver.now = driver.now.Add(schedulerStep)
	if err := runtime.Advance(driver.now); err != nil {
		load.tb.Fatal(err)
	}
	load.ticks += runtime.race.tick - tick
	load.steps++
	for drained := false; !drained; {
		select {
		case frame := <-runtime.Frames():
			if load.onFrame != nil {
				load.onFrame(frame)
			}
		case event := <-runtime.Events():
			if event.Err != nil {
				load.refused++
			}
		case <-runtime.Results():
		default:
			drained = true
		}
	}
}

// BenchmarkFausSwitchSession8 is the CPU cost of one full room. One op is a
// 60 Hz scheduler step (two physics ticks) with its flips, heartbeats and
// frame; ns/tick and allocs/tick divide by the ticks actually stepped, and
// cpu% is the share of one core a room takes at the real-time rate.
func BenchmarkFausSwitchSession8(b *testing.B) {
	load := newLoadedRace(b, MaximumPlayers)
	var before, after, pausedBefore, pausedAfter goruntime.MemStats
	var pausedMallocs uint64
	goruntime.ReadMemStats(&before)
	b.ResetTimer()
	for range b.N {
		if !load.running() {
			b.StopTimer()
			goruntime.ReadMemStats(&pausedBefore)
			load.restart()
			goruntime.ReadMemStats(&pausedAfter)
			pausedMallocs += pausedAfter.Mallocs - pausedBefore.Mallocs
			b.StartTimer()
		}
		load.step()
	}
	b.StopTimer()
	goruntime.ReadMemStats(&after)
	ticks := float64(load.ticks)
	stepNanoseconds := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/ticks, "ns/tick")
	b.ReportMetric(float64(after.Mallocs-before.Mallocs-pausedMallocs)/ticks, "allocs/tick")
	b.ReportMetric(stepNanoseconds*SchedulingRateHz/1e7, "cpu%")
	b.ReportMetric(float64(load.flips)/(float64(load.steps)/SchedulingRateHz)/MaximumPlayers, "flips/s/robot")
	b.ReportMetric(float64(load.refused), "refused")
}

// BenchmarkFausSwitchCatchUpStep is the longest step the scheduler accepts:
// it covers the whole catch-up window at once and then applies a flip of
// every robot from the start of it, the deepest rewind there is. Its cost is
// the margin left under runtimeOverloaded, which ends a race whose scheduler
// falls further behind than maximumCatchUp.
func BenchmarkFausSwitchCatchUpStep(b *testing.B) {
	load := newLoadedRace(b, MaximumPlayers)
	catchUp := maximumCatchUp - time.Millisecond
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		if !load.running() {
			load.restart()
		}
		driver, runtime := load.driver, load.driver.runtime
		tick := runtime.race.tick
		for seat, id := range load.players {
			load.sequences[seat]++
			input := driver.input(id, InputFlip)
			input.InputSequence, input.ClientTick = load.sequences[seat], tick
			// The rate window is per second; a fresh window keeps every flip.
			runtime.controls[seat].rateStartedAt = time.Time{}
			if err := runtime.Submit(input, driver.now); err != nil {
				b.Fatal(err)
			}
			_ = runtime.Submit(driver.input(id, InputHeartbeat), driver.now)
		}
		driver.now = driver.now.Add(catchUp)
		b.StartTimer()
		if err := runtime.Advance(driver.now); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if runtime.race.tick-tick < int64(catchUp*PhysicsRateHz/time.Second) {
			b.Fatalf("stepped %d ticks", runtime.race.tick-tick)
		}
		driver.drain()
		b.StartTimer()
	}
}

func longLoad() bool { return os.Getenv("HOUSEFLOW_REALTIME_LOAD") == "true" }

// deflated is the permessage-deflate size of one message as the gateway
// sends it (BestSpeed, no context takeover).
func deflated(payload []byte) int {
	var buffer bytes.Buffer
	writer, _ := flate.NewWriter(&buffer, flate.BestSpeed)
	_, _ = writer.Write(payload)
	_ = writer.Close()
	return buffer.Len()
}

// The full room's snapshots at 20 Hz for eight viewers fit the gateway's
// frame budget. HOUSEFLOW_REALTIME_LOAD=true measures a whole 120 s race
// worth of steps instead of 10 s.
func TestFausSwitchSnapshotBandwidth(t *testing.T) {
	duration := 10 * time.Second
	if testing.Short() {
		duration = 2 * time.Second
	} else if longLoad() {
		duration = MaximumMatchDuration
	}
	load := newLoadedRace(t, MaximumPlayers)
	game := Module()
	var roomEvents, wire, compressed []int
	var fanout time.Duration
	load.onFrame = func(frame RuntimeFrame) {
		payload, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		roomEvents = append(roomEvents, len(payload))
		started := time.Now()
		view, err := game.PrepareFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		for seat, id := range load.players {
			grant := load.driver.grants[id]
			rendered, err := view.Render(module.Viewer{PlayerID: id, ConnectionID: load.driver.connections[id], Grant: &grant})
			if err != nil {
				t.Fatal(err)
			}
			wire = append(wire, len(rendered))
			if seat == 0 {
				compressed = append(compressed, deflated(rendered))
			}
		}
		fanout += time.Since(started)
	}
	for steps := int(duration / schedulerStep); load.steps < steps; {
		if !load.running() {
			load.restart()
		}
		load.step()
	}
	seconds := float64(load.steps) / SchedulingRateHz
	frames := float64(len(roomEvents))
	mean := func(values []int) int {
		total := 0
		for _, value := range values {
			total += value
		}
		return total / len(values)
	}
	wireMean, deflatedMean := mean(wire), mean(compressed)
	t.Logf("%d races, %.0f s: %.1f frames/s; room event mean %d max %d B; wire/connection mean %d max %d B (deflated mean %d B); "+
		"per connection %.1f KB/s raw, %.1f KB/s deflated; room egress (8 viewers) %.1f KB/s raw; fanout(8) mean %v; flips %d, refused %d",
		load.races, seconds, frames/seconds, mean(roomEvents), slices.Max(roomEvents), wireMean, slices.Max(wire), deflatedMean,
		float64(wireMean)*frames/seconds/1024, float64(deflatedMean)*frames/seconds/1024, float64(wireMean)*frames/seconds*MaximumPlayers/1024,
		fanout/time.Duration(len(roomEvents)), load.flips, load.refused)
	// Critical changes publish extra frames; the rate is a floor, not a cap.
	if rate := frames / seconds; rate < SnapshotRateHz-1 || rate > SnapshotRateHz*1.5 {
		t.Fatalf("published %.1f frames/s", rate)
	}
	if load.refused != 0 {
		t.Fatalf("%d flips refused", load.refused)
	}
	if slices.Max(wire) > 8*1024 || slices.Max(roomEvents) > 16*1024 {
		t.Fatalf("frames too large: wire %d room event %d", slices.Max(wire), slices.Max(roomEvents))
	}
}
