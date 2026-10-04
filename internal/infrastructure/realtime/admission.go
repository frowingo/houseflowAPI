package realtime

import "time"

// A bounded-time ticket bridges HTTP upgrade and the hijacked socket handler.
// All fields are protected by the gateway mutex; concurrent upgrades count too.
type connectionReservation struct {
	gateway  *Gateway
	released bool
	timer    *time.Timer
}

func (gateway *Gateway) reserveConnection() (*connectionReservation, error) {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.closed || !gateway.started {
		return nil, ErrGatewayStopped
	}
	if gateway.options.MaxConnections > 0 && len(gateway.clients)+len(gateway.reservations) >= gateway.options.MaxConnections {
		gateway.options.Metrics.admissionRejected.Add(1)
		return nil, ErrRealtimeCapacity
	}
	reservation := &connectionReservation{gateway: gateway}
	gateway.reservations[reservation] = struct{}{}
	reservation.timer = time.AfterFunc(gateway.options.UpgradeTimeout, reservation.release)
	return reservation, nil
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
