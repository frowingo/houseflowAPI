package houseTanks

import "houseflowApi/internal/application/game/module"

const (
	// SelectColorMessageType is a lobby intent: no control is needed, and it is
	// accepted only while the session is in the lobby or its ready window.
	SelectColorMessageType      = "houseTanks.selectColor"
	PressMessageType            = "houseTanks.press"
	RoundReadyMessageType       = "houseTanks.roundReady"
	ResyncMessageType           = "houseTanks.resync"
	ControlGrantedMessageType   = "houseTanks.controlGranted"
	SnapshotMessageType         = "houseTanks.snapshot"
	ResultMessageType           = "houseTanks.result"
	InvalidInputErrorCode       = "houseTanks.error.invalid_input"
	ControlUnavailableErrorCode = "houseTanks.error.control_unavailable"
	StaleControlErrorCode       = "houseTanks.error.stale_control"
	PlayerEliminatedErrorCode   = "houseTanks.error.player_eliminated"
	NotRunningErrorCode         = "houseTanks.error.not_running"
	NotInLobbyErrorCode         = "houseTanks.error.not_in_lobby"
	ResultNotFoundErrorCode     = "houseTanks.error.result_not_found"
)

var ErrInvalidInput = module.ErrInvalidInput
