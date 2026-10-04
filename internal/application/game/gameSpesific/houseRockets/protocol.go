package houseRockets

import "errors"

const (
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
	ResultNotFoundErrorCode     = "houseRockets.error.result_not_found"
)

var ErrInvalidInput = errors.New(InvalidInputErrorCode)
