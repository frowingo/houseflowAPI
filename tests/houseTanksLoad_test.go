package tests

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/application/game/module"
)

// tanksLoadReport is one snapshot rate's cost for a full room under fire.
type tanksLoadReport struct {
	rateHz                       int
	framesPerSecond              float64
	stepMean, stepP99, stepMax   time.Duration
	roomEventMean, roomEventMax  int
	wireMean, wireMax            int
	fanoutMean                   time.Duration
	projectilesMax, eventsMax    int
	wireBytesPerSecondConnection float64
	deflatedMean                 int
	// sections are the JSON sizes of the largest wire frame's parts.
	sections map[string]int
}

// deflated is the per-message permessage-deflate size the gateway sends to a
// client that negotiated compression (fasthttp's default, BestSpeed, no
// context takeover).
func deflated(payload []byte) int {
	var buffer bytes.Buffer
	writer, _ := flate.NewWriter(&buffer, flate.BestSpeed)
	_, _ = writer.Write(payload)
	_ = writer.Close()
	return buffer.Len()
}

func wireSections(payload []byte) map[string]int {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(payload, &fields)
	sections := map[string]int{}
	for name, value := range fields {
		switch name {
		case "players", "tanks", "crates", "projectiles", "events":
			sections[name] = len(value)
		default:
			sections["other"] += len(name) + len(value) + 4
		}
	}
	return sections
}

// measureTanksLoad plays eight tanks for the duration at the 60 Hz scheduler
// rate. Half hold rapid fire, half spread shot, all tap every 10 ticks. Armor
// and power-ups are set once and never run out, so the round stays at its
// heaviest; their larger numbers add a few bytes to every frame.
func measureTanksLoad(t *testing.T, rateHz int, duration time.Duration) tanksLoadReport {
	t.Helper()
	settings := houseTanks.DefaultSettings()
	settings.SnapshotRateHz = rateHz
	now := time.Now().UTC().Truncate(time.Millisecond)
	session := houseTanks.RuntimeSession{SessionID: "tanks-load-session", HouseID: "tanks-load-house", StartedAt: now}
	for index := range houseTanks.MaximumPlayers {
		session.Players = append(session.Players, houseTanks.RuntimePlayer{PlayerID: fmt.Sprintf("player-%d", index), DisplayName: fmt.Sprintf("Player %d", index)})
	}
	runtime, err := houseTanks.NewRuntimeWithSettings(settings, session, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	driver := attachTanksDriver(t, runtime, 1, now)
	viewers := make([]module.Viewer, 0, len(session.Players))
	for _, player := range session.Players {
		grant := driver.bind(player.PlayerID, "connection-"+player.PlayerID)
		viewers = append(viewers, module.Viewer{PlayerID: player.PlayerID, ConnectionID: "connection-" + player.PlayerID, Grant: &grant})
	}
	driver.untilRoundPhase(houseTanks.RoundPlaying)
	driver.read(func(match *houseTanks.Match) {
		for index := range match.World.Tanks {
			kind := houseTanks.PowerUpRapidFire
			if index%2 == 1 {
				kind = houseTanks.PowerUpSpreadShot
			}
			match.World.Tanks[index].Armor = 999
			match.World.Tanks[index].PowerUp = &houseTanks.PowerUpEffect{Kind: kind, ExpiresAtTick: match.Tick + 1_000_000}
		}
	})
	// Reading the match marks the runtime changed, so the loop counts ticks
	// itself rather than publishing a frame every step.
	tick := driver.match().Tick + 1
	driver.advance()
	game := houseTanks.Module()
	report := tanksLoadReport{rateHz: rateHz}
	var steps []time.Duration
	frames, roomEventTotal, wireTotal, deflatedTotal := 0, 0, 0, 0
	var fanoutTotal time.Duration
	step := time.Second / houseTanks.SchedulingRateHz
	stepCount := int(duration / step)
	for index := range stepCount {
		for seat, player := range session.Players {
			// Staggered taps: a press every 10 ticks per tank, released 5 later.
			phase := (tick + int64(seat)*3) % 10
			if phase < 2 {
				_ = driver.press(player.PlayerID, true, tick, 0)
			} else if phase >= 5 && phase < 7 {
				_ = driver.press(player.PlayerID, false, tick, 0)
			}
			if index%houseTanks.SchedulingRateHz == 0 {
				_ = runtime.Submit(driver.input(player.PlayerID, houseTanks.InputHeartbeat), driver.now)
			}
		}
		driver.now = driver.now.Add(step)
		started := time.Now()
		if err := runtime.Advance(driver.now); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, time.Since(started))
		tick += houseTanks.PhysicsRateHz / houseTanks.SchedulingRateHz
		select {
		case frame := <-runtime.Frames():
			payload, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			frames++
			roomEventTotal += len(payload)
			report.roomEventMax = max(report.roomEventMax, len(payload))
			report.projectilesMax = max(report.projectilesMax, len(frame.World.Projectiles))
			report.eventsMax = max(report.eventsMax, len(frame.World.Events))
			// Gateway work: decode the room event once, render every viewer.
			fanoutStarted := time.Now()
			view, err := game.PrepareFrame(payload)
			if err != nil {
				t.Fatal(err)
			}
			for _, viewer := range viewers {
				rendered, err := view.Render(viewer)
				if err != nil {
					t.Fatal(err)
				}
				wireTotal += len(rendered)
				if len(rendered) > report.wireMax {
					report.wireMax, report.sections = len(rendered), wireSections(rendered)
				}
			}
			fanoutTotal += time.Since(fanoutStarted)
			first, _ := view.Render(viewers[0])
			deflatedTotal += deflated(first)
		default:
		}
		driver.drain()
	}
	if phase := driver.match().RoundPhase; phase != houseTanks.RoundPlaying {
		t.Fatalf("the round under fire ended: %s", phase)
	}
	slices.Sort(steps)
	var total time.Duration
	for _, value := range steps {
		total += value
	}
	report.stepMean, report.stepP99, report.stepMax = total/time.Duration(len(steps)), steps[len(steps)*99/100], steps[len(steps)-1]
	report.framesPerSecond = float64(frames) / duration.Seconds()
	report.roomEventMean = roomEventTotal / frames
	report.wireMean = wireTotal / (frames * len(viewers))
	report.fanoutMean = fanoutTotal / time.Duration(frames)
	report.deflatedMean = deflatedTotal / frames
	report.wireBytesPerSecondConnection = float64(wireTotal) / float64(len(viewers)) / duration.Seconds()
	return report
}

// The full room under heavy fire stays inside the frame budget at both
// snapshot rates. HOUSEFLOW_REALTIME_LOAD=true measures 30 s instead of 3 s.
func TestHouseTanksFrameBudgetUnderFire(t *testing.T) {
	duration := 3 * time.Second
	if os.Getenv("HOUSEFLOW_REALTIME_LOAD") == "true" {
		duration = 30 * time.Second
	}
	for _, rate := range []int{20, 30} {
		report := measureTanksLoad(t, rate, duration)
		t.Logf("%d Hz: frames/s %.1f; step mean %v p99 %v max %v; room event mean %d max %d B; wire/connection mean %d max %d B, %.1f KB/s; fanout(8) mean %v; projectiles max %d, events max %d; deflated mean %d B; largest frame sections %v",
			report.rateHz, report.framesPerSecond, report.stepMean, report.stepP99, report.stepMax, report.roomEventMean, report.roomEventMax,
			report.wireMean, report.wireMax, report.wireBytesPerSecondConnection/1024, report.fanoutMean, report.projectilesMax, report.eventsMax, report.deflatedMean, report.sections)
		// Critical changes publish extra frames; the rate is a floor, not a cap.
		if report.framesPerSecond < float64(rate)-1 || report.framesPerSecond > float64(rate)*1.5 {
			t.Fatalf("%d Hz published %.1f frames/s", rate, report.framesPerSecond)
		}
		if report.projectilesMax < 8 || report.eventsMax == 0 {
			t.Fatal("the load did not fire")
		}
		// A frame must fit the gateway's outbound budget with room to spare.
		if report.wireMax > 32*1024 || report.roomEventMax > 48*1024 {
			t.Fatalf("frames too large: wire %d room event %d", report.wireMax, report.roomEventMax)
		}
	}
}
