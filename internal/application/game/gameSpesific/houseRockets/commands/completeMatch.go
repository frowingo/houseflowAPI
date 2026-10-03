package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type CompleteMatchCommand struct {
	cqrs.Request[houseRockets.HouseRocketsResultModel]
	Result          houseRockets.HouseRocketsResultModel
	Owner           gameAbstract.RuntimeOwner
	CancelCommandID string
	CancelUserID    string
}
type CompleteMatchHandler struct {
	sessions gameAbstract.GameSessionRepository
	matches  gameAbstract.GameMatchRepository
}

func NewCompleteMatchHandler(sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository) *CompleteMatchHandler {
	return &CompleteMatchHandler{sessions, matches}
}

func (handler *CompleteMatchHandler) Handle(ctx context.Context, command CompleteMatchCommand) (houseRockets.HouseRocketsResultModel, error) {
	session, err := handler.sessions.FindByID(ctx, command.Result.SessionID)
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	// BSON dates have millisecond precision; normalize the immutable start anchor
	// before hashing, without losing the canonical result's other wire values.
	if err := houseRockets.ValidateResult(command.Result, snapshot); err != nil {
		return houseRockets.HouseRocketsResultModel{}, helpers.NewConflictError("houseRockets.error.invalid_result")
	}
	command.Result.StartedAt = &snapshot.StartedAt
	if command.Result.EndedAt.Before(snapshot.UpdatedAt) {
		command.Result.EndedAt = snapshot.UpdatedAt
	}
	if command.Owner.SessionID != snapshot.SessionID || command.Owner.Generation <= 0 || command.Owner.OwnerInstanceID == "" || command.Owner.LeaseID == "" {
		return houseRockets.HouseRocketsResultModel{}, gameAbstract.ErrRuntimeOwnerConflict
	}
	descriptor := gameAbstract.CommandDescriptor{CommandID: "complete:" + snapshot.SessionID, ActorID: gameCommands.GameSessionRuntimeActorID, CommandType: "houseRockets.completeMatch", PayloadHash: resultHash(command.Result)}
	var cancellation *gameAbstract.CommandDescriptor
	if command.CancelCommandID != "" || command.CancelUserID != "" {
		if command.CancelCommandID == "" || command.CancelUserID == "" || command.Result.Status != houseRockets.ResultCancelled || command.Result.EndReason != houseRockets.EndCancelledByUser {
			return houseRockets.HouseRocketsResultModel{}, houseRockets.ErrInvalidInput
		}
		payload, _ := json.Marshal(struct{ SessionID string }{snapshot.SessionID})
		digest := sha256.Sum256(payload)
		cancellation = &gameAbstract.CommandDescriptor{CommandID: command.CancelCommandID, ActorID: command.CancelUserID, CommandType: gameCommands.CancelGameSessionCommandType, PayloadHash: hex.EncodeToString(digest[:])}
	}
	version := snapshot.Version
	if snapshot.State == gameDomain.SessionRunning {
		if command.Result.Status == houseRockets.ResultCompleted {
			err = session.Finish(string(command.Result.EndReason), command.Result.EndedAt)
		} else {
			err = session.Cancel(string(command.Result.EndReason), command.Result.EndedAt)
		}
		if err != nil {
			return houseRockets.HouseRocketsResultModel{}, gameApplication.MapError(err)
		}
	}
	stored, err := houseRockets.EncodeMatchResult(command.Result)
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	result, err := handler.matches.Complete(ctx, gameAbstract.MatchCompletion{ExpectedVersion: version, Session: session.Snapshot(), Events: session.PendingEvents(), Result: stored, Owner: command.Owner, Command: descriptor, TriggerCommand: cancellation, ResultEventType: houseRockets.ResultMessageType})
	if errors.Is(err, gameAbstract.ErrMatchResultConflict) {
		err = helpers.NewConflictError("houseRockets.error.result_conflict")
	}
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, gameApplication.MapError(err)
	}
	return houseRockets.DecodeMatchResult(result)
}

func resultHash(result houseRockets.HouseRocketsResultModel) string {
	payload, _ := json.Marshal(result)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
