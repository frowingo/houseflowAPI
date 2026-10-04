package abstract

import "context"

type ActiveGameSession struct {
	SessionID string
	GameKey   string
}

// Pages the existing active-pointer index, never the full historical session collection.
type ActiveGameSessionScanner interface {
	ListActiveSessions(ctx context.Context, afterSessionID string, limit int) ([]ActiveGameSession, error)
}
