package abstract

import "time"

// Observation contains bounded, server-side samples, never player identities.
// Input age starts at authenticated server ingress; it is not mobile RTT.
type RuntimeObservation struct {
	StepGap         time.Duration
	StepDuration    time.Duration
	InputAges       []time.Duration
	FramesCoalesced uint64
	InputsCoalesced uint64
	Overloaded      bool
}

// Called outside the physics mutex. Implementations must be non-blocking and
// must not perform storage, network I/O or log per physics tick.
type RuntimeObserver interface{ ObserveRuntime(RuntimeObservation) }
