package game

import (
	"errors"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
)

const FlappyBirdGameKey = "flappyBird"

type Catalog struct {
	definitions map[string]gameDomain.GameDefinition
}

func NewCatalog(definitions ...gameDomain.GameDefinition) (*Catalog, error) {
	catalog := &Catalog{
		definitions: make(map[string]gameDomain.GameDefinition, len(definitions)),
	}
	for _, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return nil, err
		}
		if _, exists := catalog.definitions[definition.GameKey]; exists {
			return nil, errors.New("duplicate game definition")
		}
		catalog.definitions[definition.GameKey] = definition
	}
	return catalog, nil
}

func NewDefaultCatalog() (*Catalog, error) {
	return NewCatalog(gameDomain.GameDefinition{
		GameKey:         FlappyBirdGameKey,
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      2,
			MaximumPlayers:      8,
			ReadyWindowDuration: 30 * time.Second,
			CountdownDuration:   3 * time.Second,
		},
	})
}

func (catalog *Catalog) Find(gameKey string) (gameDomain.GameDefinition, error) {
	definition, exists := catalog.definitions[gameKey]
	if !exists {
		return gameDomain.GameDefinition{}, gameAbstract.ErrGameDefinitionNotFound
	}
	return definition, nil
}

var _ gameAbstract.GameCatalog = (*Catalog)(nil)
