package domain

type GameDefinition struct {
	GameKey         string
	ProtocolVersion int
	Mode            GameMode
	Rules           SessionRules
}

func (definition GameDefinition) Validate() error {
	switch {
	case definition.GameKey == "":
		return ErrGameKeyRequired
	case definition.ProtocolVersion <= 0:
		return ErrProtocolVersion
	case !isSupportedGameMode(definition.Mode):
		return ErrGameMode
	default:
		return definition.Rules.Validate()
	}
}
