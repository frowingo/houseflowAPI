package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// maximumExpiredTurns bounds the turns one command resolves after a long gap
// without an owner; the next deadline resumes the catch-up.
const maximumExpiredTurns = 32

// TurnEngine keeps a turn-based game's state in step with its session inside
// the command that changed the session, so both commit together. A nil engine
// serves no turn games.
type TurnEngine struct {
	games   *module.Registry
	matches gameAbstract.GameMatchRepository
}

func NewTurnEngine(games *module.Registry, matches gameAbstract.GameMatchRepository) *TurnEngine {
	return &TurnEngine{games: games, matches: matches}
}

func (engine *TurnEngine) game(gameKey string) (module.TurnModule, bool) {
	if engine == nil {
		return nil, false
	}
	return engine.games.Turn(gameKey)
}

// settle starts the state of a session that began running, resolves turns
// whose deadline passed by now and drops players who stopped playing. A
// returned result ends the match; the final state is already on the session.
func (engine *TurnEngine) settle(session *gameDomain.GameSession, now time.Time) (*gameDomain.MatchResult, error) {
	game, exists := engine.game(session.Snapshot().GameKey)
	if !exists {
		return nil, nil
	}
	for expired := 0; ; {
		snapshot := session.Snapshot()
		if snapshot.State != gameDomain.SessionRunning {
			return nil, nil
		}
		at := laterOf(now, snapshot.UpdatedAt)
		var outcome module.TurnOutcome
		var err error
		switch {
		case len(snapshot.GameState) == 0:
			// The state begins with the match, not with the command that noticed.
			at = laterOf(snapshot.StartedAt, snapshot.UpdatedAt)
			outcome, err = game.Start(module.Turn{Session: snapshot, Now: at})
		case !snapshot.TurnDeadline.IsZero() && !now.Before(snapshot.TurnDeadline) && expired < maximumExpiredTurns:
			expired++
			at = laterOf(snapshot.TurnDeadline, snapshot.UpdatedAt)
			outcome, err = game.Expire(module.Turn{Session: snapshot, Now: at})
		default:
			outcome, err = game.Reconcile(module.Turn{Session: snapshot, Now: at})
			if err != nil {
				return nil, err
			}
			return applyTurnOutcome(session, outcome, at)
		}
		if err != nil {
			return nil, err
		}
		if result, err := applyTurnOutcome(session, outcome, at); err != nil || result != nil {
			return result, err
		}
	}
}

// move applies one player's move after resolving turns that already expired.
func (engine *TurnEngine) move(session *gameDomain.GameSession, playerID, moveType string, payload []byte, now time.Time) (*gameDomain.MatchResult, error) {
	snapshot := session.Snapshot()
	game, exists := engine.game(snapshot.GameKey)
	if !exists {
		return nil, module.ErrUnsupportedIntent
	}
	move, err := game.DecodeMove(moveType, payload)
	if err != nil {
		return nil, err
	}
	if result, err := engine.settle(session, now); err != nil || result != nil {
		return result, err
	}
	snapshot = session.Snapshot()
	if snapshot.State != gameDomain.SessionRunning {
		return nil, gameDomain.ErrInvalidSessionState
	}
	if player, found := session.Player(playerID); !found || player.State != gameDomain.PlayerPlaying {
		return nil, gameDomain.ErrPlayerNotFound
	}
	at := laterOf(now, snapshot.UpdatedAt)
	outcome, err := game.ApplyMove(module.Turn{Session: snapshot, Now: at}, playerID, move)
	if err != nil {
		return nil, err
	}
	return applyTurnOutcome(session, outcome, at)
}

// applyTurnOutcome records a changed state; an unchanged one adds no version.
func applyTurnOutcome(session *gameDomain.GameSession, outcome module.TurnOutcome, at time.Time) (*gameDomain.MatchResult, error) {
	state, err := helpers.CanonicalJSONObject(outcome.State)
	if err != nil || state == nil {
		return nil, errors.Join(gameDomain.ErrGameStateRequired, err)
	}
	deadline := outcome.Deadline
	if outcome.Result != nil {
		deadline = time.Time{}
	}
	snapshot := session.Snapshot()
	if !bytes.Equal(state, snapshot.GameState) || !deadline.Equal(snapshot.TurnDeadline) {
		if err := session.UpdateGameState(state, deadline, at); err != nil {
			return nil, err
		}
	}
	return outcome.Result, nil
}

// commit stores the command's change: a state update with the command's
// receipt, or the terminal transition with its immutable result.
func (engine *TurnEngine) commit(ctx context.Context, repository gameAbstract.GameSessionRepository, session *gameDomain.GameSession, expectedVersion int64, descriptor gameAbstract.CommandDescriptor, proposed *gameDomain.MatchResult) (gameDomain.SessionSnapshot, error) {
	if proposed == nil {
		return saveSession(ctx, repository, session, expectedVersion, descriptor)
	}
	snapshot := session.Snapshot()
	game, exists := engine.game(snapshot.GameKey)
	if !exists {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(gameAbstract.ErrGameDefinitionNotFound)
	}
	result, err := game.FinalizeResult(*proposed, snapshot)
	if err != nil {
		return gameDomain.SessionSnapshot{}, helpers.NewConflictError(module.ErrorCode(snapshot.GameKey, "invalid_result"))
	}
	if err := endSession(session, result); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	var trigger *gameAbstract.CommandDescriptor
	if !descriptor.IsSystem() {
		trigger = &descriptor
	}
	digest := sha256.Sum256(result.Payload)
	_, err = engine.matches.Complete(ctx, gameAbstract.MatchCompletion{
		ExpectedVersion: expectedVersion,
		Session:         session.Snapshot(),
		Events:          session.PendingEvents(),
		Result:          result,
		Command:         gameAbstract.CommandDescriptor{CommandID: "complete:" + snapshot.SessionID, ActorID: gameAbstract.SystemActorID, CommandType: snapshot.GameKey + ".completeMatch", PayloadHash: hex.EncodeToString(digest[:])},
		TriggerCommand:  trigger,
		ResultEventType: module.ResultMessageType(snapshot.GameKey),
	})
	if errors.Is(err, gameAbstract.ErrMatchResultConflict) {
		return gameDomain.SessionSnapshot{}, helpers.NewConflictError(module.ErrorCode(snapshot.GameKey, "result_conflict"))
	}
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	session.ClearPendingEvents()
	return session.Snapshot(), nil
}

// cancel ends a running turn game on a user's request with a cancelled
// result reconstructed from the roster.
func (engine *TurnEngine) cancel(session *gameDomain.GameSession, now time.Time) (*gameDomain.MatchResult, error) {
	snapshot := session.Snapshot()
	game, _ := engine.game(snapshot.GameKey)
	result, err := game.CancellationResult(snapshot, module.EndCancelledByUser, laterOf(now, snapshot.UpdatedAt))
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// mapTurnError turns a module rejection into the shared turn-lane codes.
func mapTurnError(err error) error {
	switch {
	case errors.Is(err, module.ErrNotYourTurn):
		return helpers.NewConflictError("game.error.not_your_turn")
	case errors.Is(err, module.ErrInvalidInput), errors.Is(err, module.ErrUnsupportedIntent):
		return helpers.NewLocalizedError("game.error.invalid_move")
	default:
		return gameApplication.MapError(err)
	}
}

func laterOf(first, second time.Time) time.Time {
	if first.Before(second) {
		return second.UTC()
	}
	return first.UTC()
}
