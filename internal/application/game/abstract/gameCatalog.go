package abstract

import (
	"errors"

	gameDomain "houseflowApi/internal/application/game/domain"
)

var ErrGameDefinitionNotFound = errors.New("game definition was not found")

type GameCatalog interface {
	Find(gameKey string) (gameDomain.GameDefinition, error)
}
