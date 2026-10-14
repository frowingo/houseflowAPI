package abstract

import "context"

type ActiveGameSession struct {
	SessionID string
	GameKey   string
	// Cursor is opaque; pass the last item's cursor to read the next page.
	Cursor string
}

// Pages the active-slot index, never the full historical session collection.
type ActiveGameSessionScanner interface {
	ListActiveSessions(ctx context.Context, after string, limit int) ([]ActiveGameSession, error)
}
