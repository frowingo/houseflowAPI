package jobs

import (
	"fmt"
	"sync"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

// registry maps job types to handlers. It is sealed when processing starts,
// so a handler registered too late fails at startup instead of leaving its
// jobs unprocessed.
type registry struct {
	mutex    sync.RWMutex
	handlers map[string]jobsAbstract.Handler
	sealed   bool
}

func newRegistry() *registry {
	return &registry{handlers: make(map[string]jobsAbstract.Handler)}
}

func (r *registry) Register(jobType string, handler jobsAbstract.Handler) {
	if err := jobsAbstract.ValidateType(jobType); err != nil {
		panic(err)
	}
	if handler == nil {
		panic(fmt.Sprintf("jobs: nil handler for %s", jobType))
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.sealed {
		panic(fmt.Sprintf("jobs: %s registered after job processing started", jobType))
	}
	if _, exists := r.handlers[jobType]; exists {
		panic(fmt.Sprintf("jobs: %s registered twice", jobType))
	}
	r.handlers[jobType] = handler
}

func (r *registry) seal() {
	r.mutex.Lock()
	r.sealed = true
	r.mutex.Unlock()
}

func (r *registry) lookup(jobType string) (jobsAbstract.Handler, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	handler, ok := r.handlers[jobType]
	return handler, ok
}
