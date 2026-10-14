package tests

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/data/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// gameCommandCounter counts driver commands by name. It only observes the
// monitored client used by the repositories under measurement.
type gameCommandCounter struct {
	mutex    sync.Mutex
	commands map[string]int
}

func (counter *gameCommandCounter) started(_ context.Context, started *event.CommandStartedEvent) {
	counter.mutex.Lock()
	defer counter.mutex.Unlock()
	counter.commands[started.CommandName]++
}

func (counter *gameCommandCounter) snapshot() map[string]int {
	counter.mutex.Lock()
	defer counter.mutex.Unlock()
	result := make(map[string]int, len(counter.commands))
	for name, count := range counter.commands {
		result[name] = count
	}
	return result
}

func (counter *gameCommandCounter) reset() {
	counter.mutex.Lock()
	defer counter.mutex.Unlock()
	counter.commands = map[string]int{}
}

// TestGameMatchPersistenceFootprint characterizes what one normal four-player
// House Rockets match leaves in MongoDB. The expected counts are a deliberate
// baseline: storage optimizations must update them together with the change.
func TestGameMatchPersistenceFootprint(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	players := []string{fixture.ownerID, fixture.memberID, primitive.NewObjectID().Hex(), primitive.NewObjectID().Hex()}
	houseID, err := primitive.ObjectIDFromHex(fixture.houseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection("House").UpdateByID(fixture.ctx, houseID, bson.M{"$set": bson.M{"memberIds": players, "maxMemberCount": len(players)}}); err != nil {
		t.Fatal(err)
	}

	counter := &gameCommandCounter{commands: map[string]int{}}
	client, err := mongo.Connect(fixture.ctx, options.Client().
		ApplyURI(os.Getenv("HOUSEFLOW_TEST_MONGO_URI")).
		SetServerSelectionTimeout(5*time.Second).
		SetMonitor(&event.CommandMonitor{Started: counter.started}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	sessions := database.NewGameSessionRepository(client, fixture.db.Name())
	matches := database.NewGameMatchRepository(client, fixture.db.Name())
	catalog, err := gameApplication.NewCatalog(houseRockets.Definition())
	if err != nil {
		t.Fatal(err)
	}
	policy := fixture.membershipPolicy()
	counter.reset()

	snapshot, err := gameCommands.NewEnsureActiveGameSessionHandler(sessions, catalog, policy).Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: "footprint-create", UserID: players[0], HouseID: fixture.houseID, GameKey: houseRockets.GameKey})
	if err != nil {
		t.Fatal(err)
	}
	join := gameCommands.NewJoinGameSessionHandler(sessions, policy)
	ready := gameCommands.NewSetPlayerReadyHandler(sessions, policy)
	for index, playerID := range players {
		if snapshot, err = join.Handle(fixture.ctx, gameCommands.JoinGameSessionCommand{CommandID: fmt.Sprintf("footprint-join-%d", index), SessionID: snapshot.SessionID, UserID: playerID}); err != nil {
			t.Fatal(err)
		}
	}
	for index, playerID := range players {
		if snapshot, err = ready.Handle(fixture.ctx, gameCommands.SetPlayerReadyCommand{CommandID: fmt.Sprintf("footprint-ready-%d", index), SessionID: snapshot.SessionID, UserID: playerID, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	advance := gameCommands.NewAdvanceGameSessionHandler(sessions)
	for snapshot.State == gameDomain.SessionReadyWindow || snapshot.State == gameDomain.SessionCountdown {
		at := snapshot.CountdownEndsAt
		if snapshot.State == gameDomain.SessionReadyWindow {
			at = snapshot.ReadyWindowEndsAt
		}
		if snapshot, err = advance.Handle(fixture.ctx, gameCommands.AdvanceGameSessionCommand{CommandID: fmt.Sprintf("advance:%s:%d", snapshot.SessionID, at.UnixNano()), SessionID: snapshot.SessionID, AdvanceAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot.State != gameDomain.SessionRunning {
		t.Fatalf("session state = %s, want running", snapshot.State)
	}

	owners := gameAbstract.RuntimeOwnerRepository(sessions)
	owner, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: "footprint-owner", LeaseID: "footprint-lease"})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	identities := make([]houseRockets.PlayerIdentity, len(players))
	for index, playerID := range players {
		identities[index] = houseRockets.PlayerIdentity{PlayerID: playerID, DisplayName: fmt.Sprintf("Player %d", index)}
	}
	simulation, err := houseRockets.NewSimulation(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: identities})
	if err != nil {
		t.Fatal(err)
	}
	if err := simulation.EliminatePlayers(players[1:], houseRockets.EliminationForfeit); err != nil {
		t.Fatal(err)
	}
	result, err := simulation.ProposeResult(snapshot.StartedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRocketsCompletionHandler(sessions, matches).Handle(fixture.ctx, rocketsCompletion{Result: result, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	commands := counter.snapshot()

	expected := map[string]int64{
		"GameSession":               1,
		"GameSessionCommandReceipt": 9,
		"GameRuntimeOwner":          1,
		"GameMatchResult":           1,
	}
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	report := make([]string, 0, len(names))
	var documents int64
	for _, name := range names {
		count, err := fixture.db.Collection(name).CountDocuments(fixture.ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		documents += count
		report = append(report, fmt.Sprintf("%s=%d", name, count))
		if count != expected[name] {
			t.Errorf("%s documents = %d, want %d", name, count, expected[name])
		}
	}
	commandNames := make([]string, 0, len(commands))
	for name := range commands {
		commandNames = append(commandNames, name)
	}
	sort.Strings(commandNames)
	commandReport := make([]string, 0, len(commandNames))
	for _, name := range commandNames {
		commandReport = append(commandReport, fmt.Sprintf("%s=%d", name, commands[name]))
	}
	writes := commands["insert"] + commands["update"] + commands["delete"]
	t.Logf("MATCH_FOOTPRINT players=%d documents=%d [%s] transactions=%d writeCommands=%d commands=[%s]", len(players), documents, strings.Join(report, " "), commands["commitTransaction"], writes, strings.Join(commandReport, " "))
}
