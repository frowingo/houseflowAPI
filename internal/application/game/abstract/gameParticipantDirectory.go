package abstract

import "context"

type GameParticipant struct {
	PlayerID    string
	DisplayName string
}

// ReadHouseParticipants returns current active members, not client-provided
// names/identities. The runtime freezes a copy when countdown locks its roster.
type GameParticipantDirectory interface {
	ReadHouseParticipants(ctx context.Context, houseID string) ([]GameParticipant, error)
}
