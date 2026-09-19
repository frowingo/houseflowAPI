package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
)

func describeCommand(
	commandID string,
	actorID string,
	commandType string,
	payload any,
) (gameAbstract.CommandDescriptor, error) {
	if commandID == "" {
		return gameAbstract.CommandDescriptor{}, gameApplication.MapError(gameAbstract.ErrCommandIDRequired)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return gameAbstract.CommandDescriptor{}, err
	}
	digest := sha256.Sum256(encoded)
	return gameAbstract.CommandDescriptor{
		CommandID:   commandID,
		ActorID:     actorID,
		CommandType: commandType,
		PayloadHash: hex.EncodeToString(digest[:]),
	}, nil
}

func processedSnapshot(
	ctx context.Context,
	repository gameAbstract.GameSessionRepository,
	descriptor gameAbstract.CommandDescriptor,
) (gameDomain.SessionSnapshot, bool, error) {
	result, found, err := repository.FindProcessedCommand(ctx, descriptor)
	if err != nil {
		return gameDomain.SessionSnapshot{}, false, gameApplication.MapError(err)
	}
	if !found {
		return gameDomain.SessionSnapshot{}, false, nil
	}
	session, err := repository.FindByID(ctx, result.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, false, gameApplication.MapError(err)
	}
	return session.Snapshot(), true, nil
}

func saveSession(
	ctx context.Context,
	repository gameAbstract.GameSessionRepository,
	session *gameDomain.GameSession,
	expectedVersion int64,
	descriptor gameAbstract.CommandDescriptor,
) (gameDomain.SessionSnapshot, error) {
	result, err := repository.Save(
		ctx,
		expectedVersion,
		session.Snapshot(),
		session.PendingEvents(),
		descriptor,
	)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	if result.AlreadyProcessed {
		stored, err := repository.FindByID(ctx, result.SessionID)
		if err != nil {
			return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
		}
		return stored.Snapshot(), nil
	}
	session.ClearPendingEvents()
	return session.Snapshot(), nil
}
