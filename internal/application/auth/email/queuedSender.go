// Package email sends the authentication code emails through the background
// job queue, so a slow or failing mail server no longer holds the request.
package email

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

const SendJobType = "fauscrew:email:send"

// The payload's wire format. Field names are stable: during a rolling deploy
// a job enqueued by one version is run by another.
const (
	TemplateResetCode        = "resetCode"
	TemplateVerificationCode = "verificationCode"
)

type SendJobPayload struct {
	Template        string    `json:"template"`
	To              string    `json:"to"`
	Code            string    `json:"code"`
	ValidityMinutes int       `json:"validityMinutes"`
	ExpiresAt       time.Time `json:"expiresAt"`
	// Language was added later: a job without it, or one run by a version
	// that does not read it, is sent in English.
	Language string `json:"language,omitempty"`
}

const (
	// Retries run 5s, 10s, 20s, 40s, 80s apart; a code is valid for at
	// least its validity window, so later attempts would be pointless.
	sendMaxRetry = 5
	// Above the SMTP dial (10s) and send (20s) deadlines, so those fire first.
	sendTimeout = 45 * time.Second
)

// QueuedSender implements EmailSender by enqueuing a job; RunJob sends it
// with the direct sender. When the queue is unreachable it sends directly, as
// before, so nobody is left without a code.
//
// Mail jobs are never archived: their payload holds an address and a code.
// Completed jobs are not retained, and a job that is undeliverable, expired or
// out of retries is logged and dropped.
type QueuedSender struct {
	queue  jobsAbstract.JobQueue
	direct authAbstract.EmailSender
}

func NewQueuedSender(queue jobsAbstract.JobQueue, direct authAbstract.EmailSender) *QueuedSender {
	return &QueuedSender{queue: queue, direct: direct}
}

// Register binds the send job to this sender; call it before jobs start.
func (s *QueuedSender) Register(registry jobsAbstract.HandlerRegistry) {
	registry.Register(SendJobType, s.RunJob)
}

func (s *QueuedSender) SendResetCodeEmail(toEmail, code string, validityMinutes int, language string) error {
	return s.enqueue(TemplateResetCode, toEmail, code, validityMinutes, language)
}

func (s *QueuedSender) SendEmailVerificationCode(toEmail, code string, validityMinutes int, language string) error {
	return s.enqueue(TemplateVerificationCode, toEmail, code, validityMinutes, language)
}

func (s *QueuedSender) enqueue(template, toEmail, code string, validityMinutes int, language string) error {
	payload := SendJobPayload{
		Template: template, To: toEmail, Code: code, ValidityMinutes: validityMinutes,
		ExpiresAt: time.Now().Add(time.Duration(validityMinutes) * time.Minute).UTC(),
		Language:  language,
	}
	job, err := jobsAbstract.NewJSONJob(SendJobType, payload)
	if err == nil {
		// The port has no context; the queue bounds the enqueue itself.
		err = s.queue.Enqueue(context.Background(), job,
			jobsAbstract.InQueue(jobsAbstract.QueueCritical),
			jobsAbstract.MaxRetry(sendMaxRetry),
			jobsAbstract.Timeout(sendTimeout),
		)
	}
	if err != nil {
		log.Printf("email job could not be enqueued, sending %s directly: %v", template, err)
		return send(s.direct, payload)
	}
	return nil
}

// RunJob is the send job's handler.
func (s *QueuedSender) RunJob(ctx context.Context, payload []byte) error {
	job, err := jobsAbstract.DecodeJSON[SendJobPayload](payload)
	if err != nil {
		log.Printf("email job dropped: %v", err)
		return nil
	}
	if !time.Now().Before(job.ExpiresAt) {
		log.Printf("email job dropped: the %s code expired before it could be sent", job.Template)
		return nil
	}
	err = send(s.direct, job)
	if err == nil {
		return nil
	}
	if errors.Is(err, errUnknownTemplate) {
		log.Printf("email job dropped: %v", err)
		return nil
	}
	if attempt, ok := jobsAbstract.AttemptFromContext(ctx); ok && attempt.Last() {
		log.Printf("email job dropped: %s not sent after %d attempt(s): %v", job.Template, attempt.Retried+1, err)
		return nil
	}
	return err
}

var errUnknownTemplate = errors.New("unknown email template")

func send(sender authAbstract.EmailSender, job SendJobPayload) error {
	switch job.Template {
	case TemplateResetCode:
		return sender.SendResetCodeEmail(job.To, job.Code, job.ValidityMinutes, job.Language)
	case TemplateVerificationCode:
		return sender.SendEmailVerificationCode(job.To, job.Code, job.ValidityMinutes, job.Language)
	default:
		return fmt.Errorf("%w %q", errUnknownTemplate, job.Template)
	}
}

var _ authAbstract.EmailSender = (*QueuedSender)(nil)
