package game

import (
	"errors"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/helpers"
)

func MapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gameAbstract.ErrGameSessionNotFound):
		return helpers.NewNotFoundError("game.error.session_not_found")
	case errors.Is(err, gameAbstract.ErrGameDefinitionNotFound):
		return helpers.NewNotFoundError("game.error.definition_not_found")
	case errors.Is(err, gameAbstract.ErrGameSessionAlreadyExists):
		return helpers.NewConflictError("game.error.session_already_exists")
	case errors.Is(err, gameAbstract.ErrConcurrentSessionUpdate):
		return helpers.NewConflictError("game.error.session_conflict")
	case errors.Is(err, gameAbstract.ErrCommandIDRequired):
		return helpers.NewLocalizedError("game.error.command_id_required")
	case errors.Is(err, gameAbstract.ErrCommandIDReused):
		return helpers.NewConflictError("game.error.command_id_reused")
	case errors.Is(err, gameDomain.ErrSessionFull):
		return helpers.NewConflictError("game.error.session_full")
	case errors.Is(err, gameDomain.ErrPlayerAlreadyJoined):
		return helpers.NewConflictError("game.error.player_already_joined")
	case errors.Is(err, gameDomain.ErrPlayerNotFound):
		return helpers.NewNotFoundError("game.error.player_not_found")
	case errors.Is(err, gameDomain.ErrJoinClosed):
		return helpers.NewConflictError("game.error.join_closed")
	case errors.Is(err, gameDomain.ErrReadyClosed):
		return helpers.NewConflictError("game.error.ready_closed")
	case errors.Is(err, gameDomain.ErrInvalidSessionState),
		errors.Is(err, gameDomain.ErrTerminalSession):
		return helpers.NewConflictError("game.error.invalid_state")
	case errors.Is(err, gameDomain.ErrGameKeyRequired),
		errors.Is(err, gameDomain.ErrProtocolVersion),
		errors.Is(err, gameDomain.ErrGameMode),
		errors.Is(err, gameDomain.ErrMinimumPlayers),
		errors.Is(err, gameDomain.ErrMaximumPlayers),
		errors.Is(err, gameDomain.ErrReadyWindowDuration),
		errors.Is(err, gameDomain.ErrCountdownDuration):
		return helpers.NewLocalizedError("game.error.invalid_definition")
	default:
		return err
	}
}

func MapActiveSessionError(err error) error {
	if errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		return helpers.NewNotFoundError("game.error.active_session_not_found")
	}
	return MapError(err)
}
