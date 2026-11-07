package fausSwitch

import "houseflowApi/internal/application/game/module"

const (
	// SelectColorMessageType is a lobby intent: no control is needed, and it is
	// accepted only while the session is in the lobby or its ready window.
	SelectColorMessageType    = "fausSwitch.selectColor"
	FlipMessageType           = "fausSwitch.flip"
	ResyncMessageType         = "fausSwitch.resync"
	ControlGrantedMessageType = "fausSwitch.controlGranted"
	SnapshotMessageType       = "fausSwitch.snapshot"
	ResultMessageType         = "fausSwitch.result"
)

const (
	InvalidInputErrorCode       = "fausSwitch.error.invalid_input"
	ControlUnavailableErrorCode = "fausSwitch.error.control_unavailable"
	StaleControlErrorCode       = "fausSwitch.error.stale_control"
	RunnerFinishedErrorCode     = "fausSwitch.error.runner_finished"
	NotRunningErrorCode         = "fausSwitch.error.not_running"
	NotInLobbyErrorCode         = "fausSwitch.error.not_in_lobby"
	ResultNotFoundErrorCode     = "fausSwitch.error.result_not_found"
)

var ErrInvalidInput = module.ErrInvalidInput
