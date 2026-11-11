package abstract

import (
	"context"

	gameDomain "houseflowApi/internal/application/game/domain"
)

type ActiveGameSession struct {
	SessionID string
	GameKey   string
	State     gameDomain.SessionState
	// Cursor is opaque; pass the last item's cursor to read the next page.
	Cursor string
}

// Pages the active-slot index, never the full historical session collection.
type ActiveGameSessionScanner interface {
	ListActiveSessions(ctx context.Context, after string, limit int) ([]ActiveGameSession, error)
}
