package adaptors

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"houseflowApi/internal/config"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
)

const jobsCloseTimeout = 10 * time.Second

// NewJobRuntime uses Asynq when Valkey is configured, like the coordinator.
// Without it, or when its URL is unusable, jobs run in this process and are
// lost on restart. The process role (FAUSCREW_ROLE) decides whether this
// instance also works the queue.
func NewJobRuntime(redisConfig config.ConfigRedis, process config.ConfigProcess) infrastructureJobs.Runtime {
	if redisConfig.URL == "" {
		log.Println("background jobs run in-process because REDIS_URL is empty; they do not survive a restart")
		return infrastructureJobs.NewInlineRuntime()
	}
	runtime, err := infrastructureJobs.NewAsynqRuntime(infrastructureJobs.AsynqOptions{
		URL:     redisConfig.URL,
		Consume: process.ConsumesJobs(),
	})
	if err != nil {
		log.Printf("job queue initialization failed; background jobs run in-process: %v", err)
		return infrastructureJobs.NewInlineRuntime()
	}
	log.Printf("job queue ready (role=%s)", process.Role)
	return runtime
}

// NewWorkerJobRuntime is the job runtime of the worker command. A worker
// exists to drain the shared queue, so unlike NewJobRuntime it does not fall
// back to in-process jobs: without Valkey it fails and the machine restarts.
func NewWorkerJobRuntime(redisConfig config.ConfigRedis) (infrastructureJobs.Runtime, error) {
	if redisConfig.URL == "" {
		return nil, errors.New("the worker needs REDIS_URL: it only processes the shared job queue")
	}
	runtime, err := infrastructureJobs.NewAsynqRuntime(infrastructureJobs.AsynqOptions{
		URL:     redisConfig.URL,
		Consume: true,
	})
	if err != nil {
		return nil, fmt.Errorf("job queue initialization failed: %w", err)
	}
	log.Printf("job queue ready (role=%s)", config.ProcessRoleWorker)
	return runtime, nil
}

func CloseJobRuntime(runtime infrastructureJobs.Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), jobsCloseTimeout)
	defer cancel()
	if err := runtime.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("background job shutdown failed: %v", err)
	}
}
