package houseTanks

import "houseflowApi/internal/application/game/module"

const (
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
	ResultNotFoundErrorCode     = "houseTanks.error.result_not_found"
)

var ErrInvalidInput = module.ErrInvalidInput
