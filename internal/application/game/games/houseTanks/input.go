package houseTanks

import (
	"time"

	"houseflowApi/internal/application/game/module"
)

const (
	InputBind       = module.InputBind
	InputPress      = "press"
	InputRoundReady = "roundReady"
	InputHeartbeat  = module.InputHeartbeat
	InputDisconnect = module.InputDisconnect
	InputSnapshot   = module.InputSnapshot
	InputLifetime   = time.Second
)

// RuntimeInput comes from a trusted authenticated transport, never directly from
// client-supplied player/connection identities. Epoch and generation are opaque.
type RuntimeInput struct {
	MessageID         string    `json:"messageId,omitempty"`
	Kind              string    `json:"kind"`
	PlayerID          string    `json:"playerId"`
	ConnectionID      string    `json:"connectionId"`
	RuntimeEpoch      int64     `json:"runtimeEpoch"`
	ControlGeneration string    `json:"controlGeneration"`
	InputSequence     int64     `json:"inputSequence"`
	Pressed           bool      `json:"pressed"`
	ClientTick        int64     `json:"clientTick"`
	Heading           float64   `json:"heading"`
	Ready             bool      `json:"ready"`
	CreatedAt         time.Time `json:"createdAt"`
}

type pressIntent struct{ HouseTanksPressModel }

func (intent pressIntent) RuntimeInput(control module.ControlInput) any {
	return RuntimeInput{MessageID: control.MessageID, Kind: InputPress, PlayerID: control.PlayerID, ConnectionID: control.ConnectionID, RuntimeEpoch: control.RuntimeEpoch,
		ControlGeneration: intent.ControlGeneration, InputSequence: intent.InputSequence, Pressed: intent.Pressed, ClientTick: intent.ClientTick, Heading: intent.Heading, CreatedAt: control.CreatedAt}
}

type roundReadyIntent struct{ HouseTanksRoundReadyModel }

func (intent roundReadyIntent) RuntimeInput(control module.ControlInput) any {
	return RuntimeInput{MessageID: control.MessageID, Kind: InputRoundReady, PlayerID: control.PlayerID, ConnectionID: control.ConnectionID, RuntimeEpoch: control.RuntimeEpoch,
		ControlGeneration: intent.ControlGeneration, Ready: intent.Ready, CreatedAt: control.CreatedAt}
}
