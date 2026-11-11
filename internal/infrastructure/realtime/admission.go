package realtime

import "time"

// maxConnectionsPerUser keeps one account from holding an instance's every
// slot. It leaves room for a reconnect while the dropped socket is still
// registered and for a second device; appEvents uses the same cap.
const maxConnectionsPerUser = 4

// A bounded-time ticket bridges HTTP upgrade and the hijacked socket handler.
// All fields are protected by the gateway mutex; concurrent upgrades count too.
type connectionReservation struct {
	gateway  *Gateway
	userID   string
	released bool
	timer    *time.Timer
}

func (gateway *Gateway) reserveConnection(userID string) (*connectionReservation, error) {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.closed || !gateway.started {
		return nil, ErrGatewayStopped
	}
	if !gateway.admitsLocked(userID) {
		gateway.options.Metrics.admissionRejected.Add(1)
		return nil, ErrRealtimeCapacity
	}
	reservation := &connectionReservation{gateway: gateway, userID: userID}
	gateway.reservations[reservation] = struct{}{}
	reservation.timer = time.AfterFunc(gateway.options.UpgradeTimeout, reservation.release)
	return reservation, nil
}

// admitsLocked reports whether the instance and the user both have a free
// slot; registered sockets and pending upgrades count alike.
func (gateway *Gateway) admitsLocked(userID string) bool {
	if gateway.options.MaxConnections > 0 && len(gateway.clients)+len(gateway.reservations) >= gateway.options.MaxConnections {
		return false
	}
	held := 0
	for _, client := range gateway.clients {
		if client.userID == userID {
			held++
		}
	}
	for reservation := range gateway.reservations {
		if reservation.userID == userID {
			held++
		}
	}
	return held < maxConnectionsPerUser
}

func (reservation *connectionReservation) release() {
	reservation.gateway.mutex.Lock()
	defer reservation.gateway.mutex.Unlock()
	reservation.releaseLocked()
}

func (reservation *connectionReservation) releaseLocked() {
	if reservation.released {
		return
	}
	reservation.released = true
	if reservation.timer != nil {
		reservation.timer.Stop()
	}
	delete(reservation.gateway.reservations, reservation)
}
