package account

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	authEmail "houseflowApi/internal/application/auth/email"
	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/tests/testkit"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

type sentEmail struct {
	Kind, To, Code  string
	ValidityMinutes int
}

// emailRecorder is a concurrency-safe EmailSender that can be made to fail.
type emailRecorder struct {
	mutex sync.Mutex
	sent  []sentEmail
	err   error
	calls chan sentEmail
}

func newEmailRecorder() *emailRecorder {
	return &emailRecorder{calls: make(chan sentEmail, 16)}
}

func (r *emailRecorder) record(kind, to, code string, validityMinutes int) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	email := sentEmail{Kind: kind, To: to, Code: code, ValidityMinutes: validityMinutes}
	r.calls <- email
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, email)
	return nil
}

func (r *emailRecorder) SendResetCodeEmail(to, code string, validityMinutes int) error {
	return r.record("reset", to, code, validityMinutes)
}

func (r *emailRecorder) SendEmailVerificationCode(to, code string, validityMinutes int) error {
	return r.record("verification", to, code, validityMinutes)
}

func (r *emailRecorder) Sent() []sentEmail {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]sentEmail(nil), r.sent...)
}

type enqueuedJob struct {
	job     jobsAbstract.Job
	options jobsAbstract.EnqueueOptions
}

type capturingQueue struct {
	jobs []enqueuedJob
	err  error
}

func (q *capturingQueue) Enqueue(_ context.Context, job jobsAbstract.Job, options ...jobsAbstract.Option) error {
	if q.err != nil {
		return q.err
	}
	resolved, err := jobsAbstract.ResolveOptions(options...)
	if err != nil {
		return err
	}
	q.jobs = append(q.jobs, enqueuedJob{job: job, options: resolved})
	return nil
}

func TestQueuedEmailSenderEnqueuesCodeEmails(t *testing.T) {
	queue := &capturingQueue{}
	direct := newEmailRecorder()
	sender := authEmail.NewQueuedSender(queue, direct)

	before := time.Now()
	if err := sender.SendResetCodeEmail("a@example.com", "ABCD12", 10); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendEmailVerificationCode("b@example.com", "EFGH34", 15); err != nil {
		t.Fatal(err)
	}
	if len(direct.Sent()) != 0 {
		t.Fatal("queued sender sent mail inside the request")
	}
	if len(queue.jobs) != 2 {
		t.Fatalf("enqueued %d jobs, want 2", len(queue.jobs))
	}

	want := []authEmail.SendJobPayload{
		{Template: authEmail.TemplateResetCode, To: "a@example.com", Code: "ABCD12", ValidityMinutes: 10},
		{Template: authEmail.TemplateVerificationCode, To: "b@example.com", Code: "EFGH34", ValidityMinutes: 15},
	}
	for index, enqueued := range queue.jobs {
		if enqueued.job.Type != authEmail.SendJobType {
			t.Errorf("job type = %q", enqueued.job.Type)
		}
		payload, err := jobsAbstract.DecodeJSON[authEmail.SendJobPayload](enqueued.job.Payload)
		if err != nil {
			t.Fatal(err)
		}
		expiresIn := payload.ExpiresAt.Sub(before)
		validity := time.Duration(want[index].ValidityMinutes) * time.Minute
		if expiresIn < validity || expiresIn > validity+time.Minute {
			t.Errorf("job %d expires in %v, want about %v", index, expiresIn, validity)
		}
		payload.ExpiresAt = time.Time{}
		if payload != want[index] {
			t.Errorf("job %d payload = %+v, want %+v", index, payload, want[index])
		}
		options := enqueued.options
		if options.Queue != jobsAbstract.QueueCritical || options.MaxRetry == nil || *options.MaxRetry != 5 ||
			options.Timeout != 45*time.Second || options.Retention != 0 || options.TaskID != "" {
			t.Errorf("job %d options = %+v", index, options)
		}
	}
}

func TestQueuedEmailSenderSendsDirectlyWhenQueueIsUnavailable(t *testing.T) {
	queue := &capturingQueue{err: errors.New("valkey unreachable")}
	direct := newEmailRecorder()
	sender := authEmail.NewQueuedSender(queue, direct)

	if err := sender.SendResetCodeEmail("a@example.com", "ABCD12", 10); err != nil {
		t.Fatal(err)
	}
	if sent := direct.Sent(); len(sent) != 1 || sent[0] != (sentEmail{"reset", "a@example.com", "ABCD12", 10}) {
		t.Fatalf("direct sends = %+v", sent)
	}

	direct.err = errors.New("smtp down")
	if err := sender.SendEmailVerificationCode("a@example.com", "EFGH34", 10); !errors.Is(err, direct.err) {
		t.Fatalf("fallback error = %v, want the SMTP error", err)
	}
}

func emailJobPayload(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// The JSON field names are the wire format between deploy versions; the
// payloads here are written by hand on purpose.
func TestEmailJobSendsWithDirectSender(t *testing.T) {
	direct := newEmailRecorder()
	sender := authEmail.NewQueuedSender(&capturingQueue{}, direct)
	expiresAt := time.Now().Add(time.Minute)

	for _, template := range []string{"resetCode", "verificationCode"} {
		payload := emailJobPayload(t, map[string]any{
			"template": template, "to": "a@example.com", "code": "ABCD12", "validityMinutes": 10, "expiresAt": expiresAt,
		})
		if err := sender.RunJob(context.Background(), payload); err != nil {
			t.Fatal(err)
		}
	}
	want := []sentEmail{{"reset", "a@example.com", "ABCD12", 10}, {"verification", "a@example.com", "ABCD12", 10}}
	if sent := direct.Sent(); len(sent) != 2 || sent[0] != want[0] || sent[1] != want[1] {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestEmailJobRetriesUntilItsLastAttempt(t *testing.T) {
	direct := newEmailRecorder()
	direct.err = errors.New("smtp down")
	sender := authEmail.NewQueuedSender(&capturingQueue{}, direct)
	payload := emailJobPayload(t, map[string]any{
		"template": "resetCode", "to": "a@example.com", "code": "ABCD12", "validityMinutes": 10,
		"expiresAt": time.Now().Add(time.Minute),
	})

	early := jobsAbstract.ContextWithAttempt(context.Background(), jobsAbstract.Attempt{Retried: 2, MaxRetry: 5})
	if err := sender.RunJob(early, payload); !errors.Is(err, direct.err) {
		t.Fatalf("failure before the last attempt = %v, want a retry", err)
	}
	// Dropped instead of archived: the payload holds an address and a code.
	last := jobsAbstract.ContextWithAttempt(context.Background(), jobsAbstract.Attempt{Retried: 5, MaxRetry: 5})
	if err := sender.RunJob(last, payload); err != nil {
		t.Fatalf("failure on the last attempt = %v, want nil", err)
	}
}

func TestEmailJobDropsExpiredUnreadableAndUnknownJobs(t *testing.T) {
	direct := newEmailRecorder()
	sender := authEmail.NewQueuedSender(&capturingQueue{}, direct)
	payloads := map[string][]byte{
		"expired": emailJobPayload(t, map[string]any{
			"template": "resetCode", "to": "a@example.com", "code": "ABCD12", "validityMinutes": 10,
			"expiresAt": time.Now().Add(-time.Second),
		}),
		"unknown template": emailJobPayload(t, map[string]any{
			"template": "newsletter", "to": "a@example.com", "code": "ABCD12", "validityMinutes": 10,
			"expiresAt": time.Now().Add(time.Minute),
		}),
		"unreadable": []byte("{"),
	}
	for name, payload := range payloads {
		if err := sender.RunJob(context.Background(), payload); err != nil {
			t.Errorf("%s: RunJob = %v, want nil", name, err)
		}
	}
	if sent := direct.Sent(); len(sent) != 0 {
		t.Fatalf("dropped jobs sent mail: %+v", sent)
	}
}

func waitForEmail(t *testing.T, direct *emailRecorder) sentEmail {
	t.Helper()
	select {
	case email := <-direct.calls:
		return email
	case <-time.After(10 * time.Second):
		t.Fatal("email was not sent")
		return sentEmail{}
	}
}

func TestQueuedEmailSenderRunsThroughInProcessRuntime(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	direct := newEmailRecorder()
	sender := authEmail.NewQueuedSender(runtime, direct)
	sender.Register(runtime)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if err := sender.SendResetCodeEmail("a@example.com", "ABCD12", 10); err != nil {
		t.Fatal(err)
	}
	if email := waitForEmail(t, direct); email != (sentEmail{"reset", "a@example.com", "ABCD12", 10}) {
		t.Fatalf("sent = %+v", email)
	}
}

func TestQueuedEmailSenderRunsThroughValkeyAndKeepsNothing(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	runtime, err := infrastructureJobs.NewAsynqRuntime(infrastructureJobs.AsynqOptions{
		URL: redisURL, Consume: true, ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	direct := newEmailRecorder()
	sender := authEmail.NewQueuedSender(runtime, direct)
	sender.Register(runtime)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	})

	if err := sender.SendEmailVerificationCode("a@example.com", "EFGH34", 10); err != nil {
		t.Fatal(err)
	}
	if email := waitForEmail(t, direct); email != (sentEmail{"verification", "a@example.com", "EFGH34", 10}) {
		t.Fatalf("sent = %+v", email)
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	inspector := asynq.NewInspectorFromRedisClient(client)
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := inspector.GetQueueInfo(string(jobsAbstract.QueueCritical))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size == 0 && info.Completed == 0 && info.Archived == 0 {
			break // the address and code are gone from Valkey
		}
		if time.Now().After(deadline) {
			t.Fatalf("critical queue still holds the email job: %+v", info)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
