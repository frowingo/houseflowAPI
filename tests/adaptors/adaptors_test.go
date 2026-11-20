package adaptors

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"houseflowApi/internal/adaptors"
	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	"houseflowApi/internal/config"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/tests/testkit"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

var allRole = config.ConfigProcess{Role: config.ProcessRoleAll}

func TestNewJobRuntimeFallsBackToInProcess(t *testing.T) {
	for name, url := range map[string]string{"no REDIS_URL": "", "unusable REDIS_URL": "://not-a-url"} {
		runtime := adaptors.NewJobRuntime(config.ConfigRedis{URL: url}, allRole)
		if _, ok := runtime.(*infrastructureJobs.InlineRuntime); !ok {
			t.Errorf("%s: runtime = %T, want in-process", name, runtime)
		}
		adaptors.CloseJobRuntime(runtime)
	}
}

func TestNewJobRuntimeUsesAsynqWhenRedisIsConfigured(t *testing.T) {
	// Building the runtime does not connect, so no Redis is needed here.
	runtime := adaptors.NewJobRuntime(config.ConfigRedis{URL: "redis://127.0.0.1:1/0"}, allRole)
	if _, ok := runtime.(*infrastructureJobs.AsynqRuntime); !ok {
		t.Fatalf("runtime = %T, want Asynq", runtime)
	}
	adaptors.CloseJobRuntime(runtime)
}

// FAUSCREW_ROLE=api enqueues but leaves the job in the queue.
func TestAPIRoleJobRuntimeDoesNotWorkTheQueue(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	runtime := adaptors.NewJobRuntime(config.ConfigRedis{URL: redisURL}, config.ConfigProcess{Role: config.ProcessRoleAPI})
	t.Cleanup(func() { adaptors.CloseJobRuntime(runtime) })
	ran := make(chan struct{}, 1)
	runtime.Register("fauscrew:test:api-role", func(context.Context, []byte) error {
		ran <- struct{}{}
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:api-role"},
		jobsAbstract.TaskID("fauscrew:test:api-role:1")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
		t.Fatal("api role processed a job")
	case <-time.After(2 * time.Second):
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	info, err := asynq.NewInspectorFromRedisClient(client).GetTaskInfo(string(jobsAbstract.QueueDefault), "fauscrew:test:api-role:1")
	if err != nil || info.State != asynq.TaskStatePending {
		t.Fatalf("job = %+v, %v; want pending", info, err)
	}
}

func TestNewWorkerJobRuntimeNeedsValkey(t *testing.T) {
	for name, url := range map[string]string{"no REDIS_URL": "", "unusable REDIS_URL": "://not-a-url"} {
		runtime, err := adaptors.NewWorkerJobRuntime(config.ConfigRedis{URL: url})
		if err == nil || runtime != nil {
			t.Errorf("%s: runtime = %T, err = %v; want an error and no in-process fallback", name, runtime, err)
		}
	}
}

// With a worker group the API only enqueues and the worker runs the job.
func TestWorkerRuntimeRunsJobsTheAPIRoleEnqueues(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	ran := make(chan string, 2)
	handler := func(name string) jobsAbstract.Handler {
		return func(context.Context, []byte) error {
			ran <- name
			return nil
		}
	}

	api := adaptors.NewJobRuntime(config.ConfigRedis{URL: redisURL}, config.ConfigProcess{Role: config.ProcessRoleAPI})
	t.Cleanup(func() { adaptors.CloseJobRuntime(api) })
	api.Register("fauscrew:test:split", handler("api"))
	if err := api.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := adaptors.NewWorkerJobRuntime(config.ConfigRedis{URL: redisURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adaptors.CloseJobRuntime(worker) })
	worker.Register("fauscrew:test:split", handler("worker"))
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := api.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:split"}); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-ran:
		if name != "worker" {
			t.Fatalf("job ran on %s", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not run the job")
	}
}

func TestNewCoordinatorIsNilWithoutUsableRedis(t *testing.T) {
	for name, url := range map[string]string{"no REDIS_URL": "", "unusable REDIS_URL": "://not-a-url"} {
		coordinator := adaptors.NewCoordinator(context.Background(), config.ConfigRedis{URL: url}, config.ConfigProcess{})
		if coordinator != nil {
			t.Errorf("%s: coordinator = %T, want nil", name, coordinator)
		}
		adaptors.CloseCoordinator(coordinator)
	}
	err := adaptors.CoordinationReadiness(nil)(context.Background())
	if !errors.Is(err, coordinationAbstract.ErrUnavailable) {
		t.Fatalf("readiness without coordinator = %v, want ErrUnavailable", err)
	}
}

func TestNewCoordinatorUsesInstanceIDFromConfig(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	coordinator := adaptors.NewCoordinator(ctx, config.ConfigRedis{URL: redisURL}, config.ConfigProcess{InstanceID: "machine-7"})
	if coordinator == nil {
		t.Fatal("coordinator is nil with a usable Redis")
	}
	t.Cleanup(func() { adaptors.CloseCoordinator(coordinator) })
	if id := coordinator.InstanceID(); len(id) <= len("machine-7") || id[:len("machine-7")] != "machine-7" {
		t.Fatalf("instance ID = %q, want it based on machine-7", id)
	}
	if err := adaptors.CoordinationReadiness(coordinator)(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNewSocialAuthAdaptersBuildsOnlyConfiguredProviders(t *testing.T) {
	adapters, err := adaptors.NewSocialAuthAdapters(config.ConfigSocialAuth{})
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters.Verifiers)+len(adapters.Keepers)+len(adapters.DeletionVerifiers)+len(adapters.Notifications) != 0 {
		t.Fatalf("adapters without configuration = %+v", adapters)
	}

	adapters, err = adaptors.NewSocialAuthAdapters(config.ConfigSocialAuth{
		Google:   config.ConfigGoogleAuth{ClientIDs: []string{"google-client"}},
		Facebook: config.ConfigFacebookAuth{AppID: "fb-app", AppSecret: "fb-secret"},
		Snapchat: config.ConfigSnapchatAuth{ClientID: "snap-client"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters.Verifiers) != 3 || len(adapters.DeletionVerifiers) != 1 ||
		len(adapters.Keepers) != 0 || len(adapters.Notifications) != 0 {
		t.Fatalf("verifiers=%d deletion=%d keepers=%d notifications=%d",
			len(adapters.Verifiers), len(adapters.DeletionVerifiers), len(adapters.Keepers), len(adapters.Notifications))
	}
}

func TestNewSocialAuthAdaptersStopsOnBrokenAppleConfiguration(t *testing.T) {
	apple := config.ConfigAppleAuth{ClientIDs: []string{"apple-client"}, TeamID: "team", KeyID: "key", PrivateKey: "not a key"}
	if _, err := adaptors.NewSocialAuthAdapters(config.ConfigSocialAuth{Apple: apple, TokenEncryptionKey: "short"}); err == nil {
		t.Error("invalid token encryption key accepted")
	}
	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := adaptors.NewSocialAuthAdapters(config.ConfigSocialAuth{Apple: apple, TokenEncryptionKey: validKey}); err == nil {
		t.Error("invalid Apple private key accepted")
	}
}

func TestObserveRealtimeReturnsWithoutRealtimeService(t *testing.T) {
	done := make(chan struct{})
	go func() {
		adaptors.ObserveRealtime(context.Background(), nil, nil, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ObserveRealtime blocked without a realtime service")
	}
}
