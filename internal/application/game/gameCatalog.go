package game

import (
	"errors"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
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

type CatalogOptions struct{ EnableHouseRockets bool }

func NewDefaultCatalog(options ...CatalogOptions) (*Catalog, error) {
	definitions := []gameDomain.GameDefinition{{
		GameKey:         FlappyBirdGameKey,
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      2,
			MaximumPlayers:      8,
			ReadyWindowDuration: 30 * time.Second,
			CountdownDuration:   3 * time.Second,
		},
	}}
	if len(options) > 0 && options[0].EnableHouseRockets {
		definitions = append(definitions, houseRockets.Definition())
	}
	return NewCatalog(definitions...)
}

func (catalog *Catalog) Find(gameKey string) (gameDomain.GameDefinition, error) {
	definition, exists := catalog.definitions[gameKey]
	if !exists {
		return gameDomain.GameDefinition{}, gameAbstract.ErrGameDefinitionNotFound
	}
	return definition, nil
}

var _ gameAbstract.GameCatalog = (*Catalog)(nil)
