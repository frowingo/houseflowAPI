package realtime

import (
	"sync/atomic"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/infrastructure/telemetry"
)

type Metrics struct {
	stepGap             telemetry.DurationHistogram
	stepDuration        telemetry.DurationHistogram
	inputAge            telemetry.DurationHistogram
	checkpointDuration  telemetry.DurationHistogram
	socketWriteDuration telemetry.DurationHistogram
	framesCoalesced     atomic.Uint64
	inputsCoalesced     atomic.Uint64
	overloads           atomic.Uint64
	checkpoints         atomic.Uint64
	checkpointFailures  atomic.Uint64
	checkpointBytes     atomic.Uint64
	roomPublishedBytes  atomic.Uint64
	socketWrittenBytes  atomic.Uint64
	inputBytes          atomic.Uint64
	slowConsumers       atomic.Uint64
	admissionRejected   atomic.Uint64
	errors              atomic.Uint64
}

type MetricsSnapshot struct {
	ActiveRooms               int                       `json:"activeRooms"`
	PendingActivations        int                       `json:"pendingActivations"`
	ActiveConnections         int                       `json:"activeConnections"`
	PendingConnections        int                       `json:"pendingConnections"`
	MaximumRooms              int                       `json:"maximumRooms"`
	MaximumConnections        int                       `json:"maximumConnections"`
	PhysicsGap                telemetry.DurationSummary `json:"physicsGap"`
	PhysicsStep               telemetry.DurationSummary `json:"physicsStep"`
	ServerInputAge            telemetry.DurationSummary `json:"serverInputAge"`
	CheckpointWrite           telemetry.DurationSummary `json:"checkpointWrite"`
	SocketWrite               telemetry.DurationSummary `json:"socketWrite"`
	FramesCoalesced           uint64                    `json:"framesCoalesced"`
	InputsCoalesced           uint64                    `json:"inputsCoalesced"`
	RuntimeOverloads          uint64                    `json:"runtimeOverloads"`
	CheckpointWrites          uint64                    `json:"checkpointWrites"`
	CheckpointFailures        uint64                    `json:"checkpointFailures"`
	CheckpointPayloadBytes    uint64                    `json:"checkpointPayloadBytes"`
	RoomPublishedPayloadBytes uint64                    `json:"roomPublishedPayloadBytes"`
	SocketWrittenPayloadBytes uint64                    `json:"socketWrittenPayloadBytes"`
	InputPayloadBytes         uint64                    `json:"inputPayloadBytes"`
	SlowConsumers             uint64                    `json:"slowConsumers"`
	AdmissionRejected         uint64                    `json:"admissionRejected"`
	Errors                    uint64                    `json:"errors"`
}

func (metrics *Metrics) ObserveRuntime(observation gameAbstract.RuntimeObservation) {
	if observation.StepDuration > 0 {
		metrics.stepGap.Observe(observation.StepGap)
		metrics.stepDuration.Observe(observation.StepDuration)
	}
	for _, age := range observation.InputAges {
		metrics.inputAge.Observe(age)
	}
	metrics.framesCoalesced.Add(observation.FramesCoalesced)
	metrics.inputsCoalesced.Add(observation.InputsCoalesced)
	if observation.Overloaded {
		metrics.overloads.Add(1)
	}
}

func (metrics *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{PhysicsGap: metrics.stepGap.Snapshot(), PhysicsStep: metrics.stepDuration.Snapshot(), ServerInputAge: metrics.inputAge.Snapshot(), CheckpointWrite: metrics.checkpointDuration.Snapshot(), SocketWrite: metrics.socketWriteDuration.Snapshot(), FramesCoalesced: metrics.framesCoalesced.Load(), InputsCoalesced: metrics.inputsCoalesced.Load(), RuntimeOverloads: metrics.overloads.Load(), CheckpointWrites: metrics.checkpoints.Load(), CheckpointFailures: metrics.checkpointFailures.Load(), CheckpointPayloadBytes: metrics.checkpointBytes.Load(), RoomPublishedPayloadBytes: metrics.roomPublishedBytes.Load(), SocketWrittenPayloadBytes: metrics.socketWrittenBytes.Load(), InputPayloadBytes: metrics.inputBytes.Load(), SlowConsumers: metrics.slowConsumers.Load(), AdmissionRejected: metrics.admissionRejected.Load(), Errors: metrics.errors.Load()}
}

func (metrics *Metrics) observeSocketWrite(start time.Time, bytes int, err error) {
	metrics.socketWriteDuration.Observe(time.Since(start))
	if err == nil {
		metrics.socketWrittenBytes.Add(uint64(bytes))
	}
}
