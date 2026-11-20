package jobs

import (
	"errors"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

func TestValidateTypeRequiresFausCrewPrefix(t *testing.T) {
	for _, valid := range []string{"fauscrew:email:send", "fauscrew:push:send"} {
		if err := jobsAbstract.ValidateType(valid); err != nil {
			t.Errorf("ValidateType(%q) = %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "fauscrew:", "email:send", "houseflow:email:send", "fauscrew:email send"} {
		if err := jobsAbstract.ValidateType(invalid); err == nil {
			t.Errorf("ValidateType(%q) accepted an invalid type", invalid)
		}
	}
}

func TestResolveOptionsDefaultsAndValidation(t *testing.T) {
	resolved, err := jobsAbstract.ResolveOptions()
	if err != nil || resolved.Queue != jobsAbstract.QueueDefault || resolved.MaxRetry != nil {
		t.Fatalf("defaults = %+v, %v", resolved, err)
	}

	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	resolved, err = jobsAbstract.ResolveOptions(jobsAbstract.InQueue(jobsAbstract.QueueCritical), jobsAbstract.MaxRetry(0),
		jobsAbstract.ProcessAt(at), jobsAbstract.Timeout(time.Second), jobsAbstract.TaskID("fauscrew:x:1"),
		jobsAbstract.Retention(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Queue != jobsAbstract.QueueCritical || *resolved.MaxRetry != 0 || !resolved.ProcessAt.Equal(at) ||
		resolved.Timeout != time.Second || resolved.TaskID != "fauscrew:x:1" || resolved.Retention != time.Minute {
		t.Fatalf("resolved = %+v", resolved)
	}

	invalid := map[string][]jobsAbstract.Option{
		"unknown queue":     {jobsAbstract.InQueue("default")},
		"negative retry":    {jobsAbstract.MaxRetry(-1)},
		"negative timeout":  {jobsAbstract.Timeout(-time.Second)},
		"unprefixed TaskID": {jobsAbstract.TaskID("deadline:1")},
	}
	for name, options := range invalid {
		if _, err := jobsAbstract.ResolveOptions(options...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJSONPayloadRoundTripAndPermanentDecodeError(t *testing.T) {
	type payload struct {
		To string `json:"to"`
	}
	job, err := jobsAbstract.NewJSONJob("fauscrew:email:send", payload{To: "a@b.c"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jobsAbstract.DecodeJSON[payload](job.Payload)
	if err != nil || decoded.To != "a@b.c" {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if _, err := jobsAbstract.DecodeJSON[payload]([]byte("{")); !errors.Is(err, jobsAbstract.ErrPermanent) {
		t.Fatalf("decode error = %v, want ErrPermanent", err)
	}
}

func TestRetryAfterUnwrapsAndAttemptLast(t *testing.T) {
	cause := errors.New("429")
	err := jobsAbstract.RetryAfter(time.Minute, cause)
	var retryAfter *jobsAbstract.RetryAfterError
	if !errors.As(err, &retryAfter) || retryAfter.Delay != time.Minute || !errors.Is(err, cause) {
		t.Fatalf("RetryAfter = %v", err)
	}
	if (jobsAbstract.Attempt{Retried: 1, MaxRetry: 2}).Last() || !(jobsAbstract.Attempt{Retried: 2, MaxRetry: 2}).Last() {
		t.Fatal("Attempt.Last is wrong")
	}
}
