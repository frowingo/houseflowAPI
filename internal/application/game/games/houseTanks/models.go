package houseTanks

import (
	"math"
	"strings"
	"time"

	"houseflowApi/internal/application/game/module"
)

// HouseTanksPressModel presses (true) or releases (false) the single button.
// ClientTick and Heading are what the client rendered when the player
// pressed; the server uses them for lag compensation only.
type HouseTanksPressModel struct {
	ControlGeneration string  `json:"controlGeneration"`
	InputSequence     int64   `json:"inputSequence"`
	Pressed           bool    `json:"pressed"`
	ClientTick        int64   `json:"clientTick"`
	Heading           float64 `json:"heading"`
}

func (model HouseTanksPressModel) Valid() bool {
	return validControlGeneration(model.ControlGeneration) && model.InputSequence > 0 && model.ClientTick >= 0 &&
		!math.IsNaN(model.Heading) && !math.IsInf(model.Heading, 0)
}

// HouseTanksRoundReadyModel is a player's vote between rounds.
type HouseTanksRoundReadyModel struct {
	ControlGeneration string `json:"controlGeneration"`
	Ready             bool   `json:"ready"`
}

func (model HouseTanksRoundReadyModel) Valid() bool {
	return validControlGeneration(model.ControlGeneration)
}

func validControlGeneration(generation string) bool {
	return generation != "" && len(generation) <= 128 && strings.TrimSpace(generation) == generation
}

type HouseTanksRuntimeSettingsModel struct {
	PhysicsRateHz                 int     `json:"physicsRateHz"`
	SchedulingRateHz              int     `json:"schedulingRateHz"`
	SnapshotRateHz                int     `json:"snapshotRateHz"`
	MaximumInputRateHz            int     `json:"maximumInputRateHz"`
	MaximumPressMessagesPerSecond int     `json:"maximumPressMessagesPerSecond"`
	ControlHeartbeatMilliseconds  int64   `json:"controlHeartbeatMilliseconds"`
	ControlTimeoutMilliseconds    int64   `json:"controlTimeoutMilliseconds"`
	ReconnectGraceMilliseconds    int64   `json:"reconnectGraceMilliseconds"`
	LagCompensationMilliseconds   int64   `json:"lagCompensationMilliseconds"`
	HeadingToleranceMilliradians  int64   `json:"headingToleranceMilliradians"`
	ArenaWidth                    float64 `json:"arenaWidth"`
	ArenaHeight                   float64 `json:"arenaHeight"`
	RoundWinsNeeded               int     `json:"roundWinsNeeded"`
	ArmorPerRound                 int     `json:"armorPerRound"`
	RoundCountdownMilliseconds    int64   `json:"roundCountdownMilliseconds"`
	RoundReadyWindowMilliseconds  int64   `json:"roundReadyWindowMilliseconds"`
	RoundReadyIdleMilliseconds    int64   `json:"roundReadyIdleMilliseconds"`
	MaximumMatchMilliseconds      int64   `json:"maximumMatchMilliseconds"`
	EventWindowMilliseconds       int64   `json:"eventWindowMilliseconds"`
}

// HouseTanksRuntimeSettings is the client view of the default settings.
func HouseTanksRuntimeSettings() HouseTanksRuntimeSettingsModel {
	return DefaultSettings().clientModel()
}

// HouseTanksWelcomeModel carries only what is fixed before a match starts; the
// map and seed are chosen at the start and arrive in every snapshot.
type HouseTanksWelcomeModel struct {
	SessionID         string                         `json:"sessionId"`
	GameKey           string                         `json:"gameKey"`
	ConnectionID      string                         `json:"connectionId"`
	ProtocolVersion   int                            `json:"protocolVersion"`
	ContractRevision  string                         `json:"contractRevision"`
	MapCatalogVersion int                            `json:"mapCatalogVersion"`
	Settings          HouseTanksRuntimeSettingsModel `json:"settings"`
}

type HouseTanksControlGrantedModel = module.ControlGrant

// HouseTanksPlayerModel is a seat of the match. IsAlive is the player's tank in
// the current round; HasLeft marks a player who left or lost membership and
// will not return, while Connected false alone may still reconnect.
type HouseTanksPlayerModel struct {
	PlayerID                   string       `json:"playerId"`
	DisplayName                string       `json:"displayName"`
	Seat                       int          `json:"seat"`
	Color                      Color        `json:"color"`
	Armor                      int          `json:"armor"`
	RoundsWon                  int          `json:"roundsWon"`
	IsAlive                    bool         `json:"isAlive"`
	Connected                  bool         `json:"connected"`
	HasLeft                    bool         `json:"hasLeft"`
	RoundReady                 bool         `json:"roundReady"`
	PowerUp                    *PowerUpKind `json:"powerUp"`
	PowerUpExpiresAtTick       *int64       `json:"powerUpExpiresAtTick"`
	LastProcessedInputSequence int64        `json:"lastProcessedInputSequence"`
	ControlGeneration          *string      `json:"controlGeneration"`
}

// HouseTanksTankModel is a tank body; a dead tank stays as a wreck.
// Controllable is false for a stationary target whose player has no control.
type HouseTanksTankModel struct {
	PlayerID          string  `json:"playerId"`
	X                 float64 `json:"x"`
	Y                 float64 `json:"y"`
	VX                float64 `json:"vx"`
	VY                float64 `json:"vy"`
	Rotation          float64 `json:"rotation"`
	RotationDirection int     `json:"rotationDirection"`
	IsDriving         bool    `json:"isDriving"`
	AimLocks          int     `json:"aimLocks"`
	Controllable      bool    `json:"controllable"`
}

// HouseTanksCrateModel is a pushable crate; Index is its obstacle index in
// the map definition.
type HouseTanksCrateModel struct {
	Index     int     `json:"index"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Destroyed bool    `json:"destroyed"`
}

type HouseTanksTokenModel struct {
	Kind          PowerUpKind `json:"kind"`
	X             float64     `json:"x"`
	Y             float64     `json:"y"`
	ExpiresAtTick int64       `json:"expiresAtTick"`
}

// HouseTanksProjectileModel is a live shell or rocket; clients extrapolate it
// along its velocity from the snapshot tick.
type HouseTanksProjectileModel struct {
	ID        int64   `json:"id"`
	OwnerID   string  `json:"ownerId"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	VX        float64 `json:"vx"`
	VY        float64 `json:"vy"`
	Rocket    bool    `json:"rocket"`
	SpawnTick int64   `json:"spawnTick"`
}

// HouseTanksEventModel is one effect of the last EventWindow. Sequence grows
// by one per event within a runtime epoch, so a client applies each once.
// Fields beyond kind are present only for the kinds that use them:
//
//	fired          playerId, angle
//	hit            playerId (target), sourceId (shooter), armor, lethal
//	shielded       playerId, sourceId
//	crateBroken    crateIndex, x, y
//	explosion      x, y
//	eliminated     playerId, sourceId, x, y
//	jokerCollected playerId, powerUp
//	roundEnded     roundNumber, winnerId (absent for a draw)
type HouseTanksEventModel struct {
	Sequence    int64        `json:"sequence"`
	Tick        int64        `json:"tick"`
	Kind        EventKind    `json:"kind"`
	PlayerID    *string      `json:"playerId,omitempty"`
	SourceID    *string      `json:"sourceId,omitempty"`
	X           *float64     `json:"x,omitempty"`
	Y           *float64     `json:"y,omitempty"`
	Angle       *float64     `json:"angle,omitempty"`
	Armor       *int         `json:"armor,omitempty"`
	Lethal      *bool        `json:"lethal,omitempty"`
	CrateIndex  *int         `json:"crateIndex,omitempty"`
	PowerUp     *PowerUpKind `json:"powerUp,omitempty"`
	RoundNumber *int         `json:"roundNumber,omitempty"`
	WinnerID    *string      `json:"winnerId,omitempty"`
}

// HouseTanksSnapshotModel is the public state of a match. RoundPhase is null
// only before the match starts (the lobby countdown preview); a recovering
// runtime keeps the round phase it restored.
type HouseTanksSnapshotModel struct {
	SessionID              string                      `json:"sessionId"`
	GameKey                string                      `json:"gameKey"`
	RuntimeEpoch           int64                       `json:"runtimeEpoch"`
	StateSequence          int64                       `json:"stateSequence"`
	Tick                   int64                       `json:"tick"`
	Phase                  Phase                       `json:"phase"`
	MapID                  MapID                       `json:"mapId"`
	MapVersion             int                         `json:"mapVersion"`
	Seed                   string                      `json:"seed"`
	RoundPhase             *RoundPhase                 `json:"roundPhase"`
	RoundNumber            int                         `json:"roundNumber"`
	CountdownEndsAt        *time.Time                  `json:"countdownEndsAt"`
	RoundCountdownEndsAt   *time.Time                  `json:"roundCountdownEndsAt"`
	RoundReadyWindowEndsAt *time.Time                  `json:"roundReadyWindowEndsAt"`
	Players                []HouseTanksPlayerModel     `json:"players"`
	Tanks                  []HouseTanksTankModel       `json:"tanks"`
	Crates                 []HouseTanksCrateModel      `json:"crates"`
	Token                  *HouseTanksTokenModel       `json:"token"`
	Projectiles            []HouseTanksProjectileModel `json:"projectiles"`
	Events                 []HouseTanksEventModel      `json:"events"`
	RoundWinnerID          *string                     `json:"roundWinnerId"`
	ChampionID             *string                     `json:"championId"`
}

type HouseTanksPlayerResultModel struct {
	PlayerID  string `json:"playerId"`
	RoundsWon int    `json:"roundsWon"`
	FinalRank *int   `json:"finalRank"`
}

// HouseTanksResultModel is the immutable outcome. Only a decided champion
// completes a match; every other end is cancelled, keeps the round scores and
// awards no ranks. MapID is null when the match never started.
type HouseTanksResultModel struct {
	SessionID       string                        `json:"sessionId"`
	HouseID         string                        `json:"houseId"`
	GameKey         string                        `json:"gameKey"`
	ProtocolVersion int                           `json:"protocolVersion"`
	MapID           *MapID                        `json:"mapId"`
	MapVersion      *int                          `json:"mapVersion"`
	Status          ResultStatus                  `json:"status"`
	EndReason       EndReason                     `json:"endReason"`
	ChampionID      *string                       `json:"championId"`
	RoundsPlayed    int                           `json:"roundsPlayed"`
	StartedAt       *time.Time                    `json:"startedAt"`
	EndedAt         time.Time                     `json:"endedAt"`
	DurationSeconds float64                       `json:"durationSeconds"`
	Players         []HouseTanksPlayerResultModel `json:"players"`
}
