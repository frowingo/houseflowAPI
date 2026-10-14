package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

// CompleteMatchCommand commits the result a runtime proposed: the session's
// terminal transition, the immutable result and the user cancellation that
// triggered it, if any, in one transaction.
type CompleteMatchCommand struct {
	cqrs.Request[gameDomain.MatchResult]
	Result          gameDomain.MatchResult
	Owner           gameAbstract.RuntimeOwner
	CancelCommandID string
	CancelUserID    string
}

type CompleteMatchHandler struct {
	sessions gameAbstract.GameSessionRepository
	matches  gameAbstract.GameMatchRepository
	games    *module.Registry
}

func NewCompleteMatchHandler(sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository, games *module.Registry) *CompleteMatchHandler {
	return &CompleteMatchHandler{sessions: sessions, matches: matches, games: games}
}

func (handler *CompleteMatchHandler) Handle(ctx context.Context, command CompleteMatchCommand) (gameDomain.MatchResult, error) {
	session, err := handler.sessions.FindByID(ctx, command.Result.SessionID)
	if err != nil {
		return gameDomain.MatchResult{}, gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	codec, exists := handler.games.Results(snapshot.GameKey)
	if !exists {
		return gameDomain.MatchResult{}, gameApplication.MapError(gameAbstract.ErrGameDefinitionNotFound)
	}
	result, err := codec.FinalizeResult(command.Result, snapshot)
	if err != nil {
		return gameDomain.MatchResult{}, helpers.NewConflictError(module.ErrorCode(snapshot.GameKey, "invalid_result"))
	}
	if !ownsSession(command.Owner, snapshot.SessionID) {
		return gameDomain.MatchResult{}, gameAbstract.ErrRuntimeOwnerConflict
	}
	cancellation, err := cancellationDescriptor(command, result)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	version := snapshot.Version
	if snapshot.State == gameDomain.SessionRunning {
		if err := endSession(session, result); err != nil {
			return gameDomain.MatchResult{}, gameApplication.MapError(err)
		}
	}
	digest := sha256.Sum256(result.Payload)
	stored, err := handler.matches.Complete(ctx, gameAbstract.MatchCompletion{
		ExpectedVersion: version,
		Session:         session.Snapshot(),
		Events:          session.PendingEvents(),
		Result:          result,
		Owner:           command.Owner,
		Command:         gameAbstract.CommandDescriptor{CommandID: "complete:" + snapshot.SessionID, ActorID: gameAbstract.SystemActorID, CommandType: snapshot.GameKey + ".completeMatch", PayloadHash: hex.EncodeToString(digest[:])},
		TriggerCommand:  cancellation,
		ResultEventType: module.ResultMessageType(snapshot.GameKey),
	})
	if errors.Is(err, gameAbstract.ErrMatchResultConflict) {
		return gameDomain.MatchResult{}, helpers.NewConflictError(module.ErrorCode(snapshot.GameKey, "result_conflict"))
	}
	if err != nil {
		return gameDomain.MatchResult{}, gameApplication.MapError(err)
	}
	return stored, nil
}

func ownsSession(owner gameAbstract.RuntimeOwner, sessionID string) bool {
	return owner.SessionID == sessionID && owner.Generation > 0 && owner.OwnerInstanceID != "" && owner.LeaseID != ""
}

// cancellationDescriptor records the user's cancel command with the result it
// produced, so a retried cancel resolves to this completion.
func cancellationDescriptor(command CompleteMatchCommand, result gameDomain.MatchResult) (*gameAbstract.CommandDescriptor, error) {
	if command.CancelCommandID == "" && command.CancelUserID == "" {
		return nil, nil
	}
	if command.CancelCommandID == "" || command.CancelUserID == "" || result.Status != gameDomain.MatchCancelled || result.EndReason != module.EndCancelledByUser {
		return nil, module.ErrInvalidInput
	}
	payload, _ := json.Marshal(struct{ SessionID string }{result.SessionID})
	digest := sha256.Sum256(payload)
	return &gameAbstract.CommandDescriptor{CommandID: command.CancelCommandID, ActorID: command.CancelUserID, CommandType: CancelGameSessionCommandType, PayloadHash: hex.EncodeToString(digest[:])}, nil
}

func endSession(session *gameDomain.GameSession, result gameDomain.MatchResult) error {
	if result.Status == gameDomain.MatchCompleted {
		return session.Finish(result.EndReason, result.EndedAt)
	}
	return session.Cancel(result.EndReason, result.EndedAt)
}

var _ cqrs.CommandHandler[CompleteMatchCommand, gameDomain.MatchResult] = (*CompleteMatchHandler)(nil)
