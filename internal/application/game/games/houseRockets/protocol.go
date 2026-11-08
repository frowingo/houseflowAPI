package houseRockets

import "houseflowApi/internal/application/game/module"

const (
	// SelectColorMessageType is a lobby intent: no control is needed, and it is
	// accepted only while the session is in the lobby or its ready window.
	SelectColorMessageType      = "houseRockets.selectColor"
	SteerMessageType            = "houseRockets.steer"
	ResyncMessageType           = "houseRockets.resync"
	ControlGrantedMessageType   = "houseRockets.controlGranted"
	SnapshotMessageType         = "houseRockets.snapshot"
	ResultMessageType           = "houseRockets.result"
	InvalidInputErrorCode       = "houseRockets.error.invalid_input"
	ControlUnavailableErrorCode = "houseRockets.error.control_unavailable"
	StaleControlErrorCode       = "houseRockets.error.stale_control"
	PlayerEliminatedErrorCode   = "houseRockets.error.player_eliminated"
	NotRunningErrorCode         = "houseRockets.error.not_running"
	NotInLobbyErrorCode         = "houseRockets.error.not_in_lobby"
	ResultNotFoundErrorCode     = "houseRockets.error.result_not_found"
)

var ErrInvalidInput = module.ErrInvalidInput
