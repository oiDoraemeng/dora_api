//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Uses an explicitly supplied test database and an isolated schema. SMTP is
// replaced with a recorder; no email leaves the test process.
func newTargetedEmailTestService(t *testing.T) *TargetedEmailDeliveryService {
	t.Helper()
	dsn := os.Getenv("TARGETED_EMAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("TARGETED_EMAIL_TEST_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("targeted_email_test_%d", time.Now().UnixNano())
	_, err = db.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); _ = db.Close() })
	testDB, err := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testDB.Close() })
	_, err = testDB.Exec(`CREATE TABLE users (id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL, deleted_at TIMESTAMPTZ)`)
	require.NoError(t, err)
	for _, file := range []string{"185_targeted_email_deliveries.sql", "252_targeted_email_jobs.sql", "253_targeted_email_user_preferences.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		require.NoError(t, err)
		_, err = testDB.Exec(string(migration))
		require.NoError(t, err)
	}
	return NewTargetedEmailDeliveryService(testDB)
}

func TestTargetedEmailJobUserBanPersistsAndSkipsQueuedMail(t *testing.T) {
	s := newTargetedEmailTestService(t)
	ctx := context.Background()
	_, err := s.db.Exec(`INSERT INTO users (id, email) VALUES (1, 'banned@example.com'), (2, 'allowed@example.com')`)
	require.NoError(t, err)
	job, err := s.CreateJob(ctx, []string{"banned@example.com", "allowed@example.com"}, "Template 1", "HTML", 5)
	require.NoError(t, err)
	// Ban an already queued recipient, then change their address: the queued
	// recipient must remain associated with the user who was selected.
	require.NoError(t, s.SetUserBlocked(ctx, 1, true))
	_, err = s.db.Exec(`UPDATE users SET email = 'new@example.com' WHERE id = 1`)
	require.NoError(t, err)
	restarted := NewTargetedEmailDeliveryService(s.db)
	statuses, err := restarted.Statuses(ctx, []string{"new@example.com"}, "different-template-hash")
	require.NoError(t, err)
	require.True(t, statuses["new@example.com"].Blocked)
	require.False(t, statuses["new@example.com"].SentCurrent, "ban must be visible without prior delivery history")
	blocked, err := restarted.IsRecipientBlocked(ctx, " New@Example.com ")
	require.NoError(t, err)
	require.True(t, blocked, "targeted test-send must respect the ban too")
	var calls []string
	restarted.sendEmail = func(_ context.Context, email, _, _ string) error { calls = append(calls, email); return nil }
	require.NoError(t, restarted.processNext(ctx))
	require.Equal(t, []string{"allowed@example.com"}, calls, "skipping banned recipients must not consume an SMTP slot")
	job, err = s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", job.Status)
	require.Equal(t, 1, job.Skipped)
	require.Equal(t, 1, job.Sent)
	require.Contains(t, job.Errors[0], "禁止接收群发邮件")
	// A later template is still banned; after unblocking it can be sent.
	_, err = s.CreateJob(ctx, []string{"new@example.com"}, "Template 2", "HTML", 5)
	require.NoError(t, err)
	targetedEmailMakeDue(t, s)
	require.NoError(t, restarted.processNext(ctx))
	require.Len(t, calls, 1)
	require.NoError(t, restarted.SetUserBlocked(ctx, 1, false))
	blocked, err = s.IsRecipientBlocked(ctx, "new@example.com")
	require.NoError(t, err)
	require.False(t, blocked)
	_, err = s.CreateJob(ctx, []string{"new@example.com"}, "Template 2", "HTML", 5)
	require.NoError(t, err)
	require.NoError(t, restarted.processNext(ctx))
	require.Equal(t, []string{"allowed@example.com", "new@example.com"}, calls)
	require.Error(t, s.SetUserBlocked(ctx, 99999, true))
}

func targetedEmailMakeDue(t *testing.T, s *TargetedEmailDeliveryService) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE targeted_email_queue_state SET next_send_at = NOW() - INTERVAL '1 second'`)
	require.NoError(t, err)
}

func TestTargetedEmailJobPacingFailureAndRestart(t *testing.T) {
	s := newTargetedEmailTestService(t)
	ctx := context.Background()
	job, err := s.CreateJob(ctx, []string{"a@example.com", "b@example.com", "c@example.com"}, "Subject", "<p>HTML</p>", 5)
	require.NoError(t, err)
	require.Equal(t, 3, job.Pending)
	_, err = s.CreateJob(ctx, []string{"d@example.com"}, "Other", "html", 5)
	require.ErrorIs(t, err, ErrTargetedEmailJobRunning)
	var calls int
	s.sendEmail = func(_ context.Context, email, subject, html string) error {
		calls++
		require.Equal(t, "Subject", subject)
		require.Equal(t, "<p>HTML</p>", html)
		if email == "b@example.com" {
			return errors.New("SMTP rate limit")
		}
		return nil
	}
	require.NoError(t, s.processNext(ctx))
	require.NoError(t, s.processNext(ctx))
	require.Equal(t, 1, calls, "polling must not bypass cooldown")
	// A new service instance recovers progress and still respects the DB cooldown.
	restarted := NewTargetedEmailDeliveryService(s.db)
	restarted.sendEmail = s.sendEmail
	job, err = restarted.GetJob(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, job.Sent)
	require.Equal(t, 2, job.Pending)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), job.NextSendAt, 5*time.Second)
	require.NoError(t, restarted.processNext(ctx))
	require.Equal(t, 1, calls)
	targetedEmailMakeDue(t, s)
	require.NoError(t, restarted.processNext(ctx))
	require.NoError(t, restarted.processNext(ctx))
	require.Equal(t, 2, calls, "failed SMTP attempts also consume the interval")
	job, err = s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, 1, job.Failed)
	require.Contains(t, job.Errors[0], "SMTP rate limit")
	targetedEmailMakeDue(t, s)
	require.NoError(t, s.processNext(ctx))
	job, err = s.GetJob(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, "completed", job.Status)
	require.Equal(t, 2, job.Sent)
	require.Equal(t, 0, job.Pending)
	require.NotNil(t, job.CompletedAt)
	statuses, err := s.Statuses(ctx, []string{"a@example.com", "b@example.com"}, TargetedEmailContentHash("Subject", "<p>HTML</p>"))
	require.NoError(t, err)
	require.True(t, statuses["a@example.com"].SentCurrent)
	require.False(t, statuses["b@example.com"].SentCurrent)
}

func TestTargetedEmailJobConcurrentWorkersAndCancel(t *testing.T) {
	s := newTargetedEmailTestService(t)
	ctx := context.Background()
	job, err := s.CreateJob(ctx, []string{"a@example.com", "b@example.com"}, "Subject", "HTML", 5)
	require.NoError(t, err)
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.sendEmail = func(context.Context, string, string, string) error {
		calls.Add(1)
		close(started)
		<-finish
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- s.processNext(ctx) }()
	<-started
	other := NewTargetedEmailDeliveryService(s.db)
	other.sendEmail = func(context.Context, string, string, string) error { calls.Add(1); return nil }
	// Even a slow SMTP attempt that outlives its reserved slot must serialize.
	targetedEmailMakeDue(t, s)
	require.NoError(t, other.processNext(ctx))
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, s.CancelJob(ctx, job.ID))
	close(finish)
	require.NoError(t, <-done)
	job, err = s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", job.Status)
	require.Equal(t, 1, job.Cancelled)
	require.Equal(t, 1, job.Sent)
	_, err = other.CreateJob(ctx, []string{"c@example.com"}, "Other", "HTML", 1)
	require.NoError(t, err)
	require.NoError(t, other.processNext(ctx))
	require.EqualValues(t, 1, calls.Load(), "new campaign must preserve previous cooldown")
}

func TestTargetedEmailJobSkips500PreviousDeliveries(t *testing.T) {
	s := newTargetedEmailTestService(t)
	ctx := context.Background()
	recipients := make([]string, 500)
	for i := range recipients {
		recipients[i] = fmt.Sprintf("user%d@example.com", i)
	}
	job, err := s.CreateJob(ctx, recipients, "Subject", "HTML", 5)
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO targeted_email_deliveries (recipient_email, content_hash, subject, status, sent_at)
		SELECT recipient_email, $1, 'Subject', 'sent', NOW() FROM targeted_email_job_recipients`, TargetedEmailContentHash("Subject", "HTML"))
	require.NoError(t, err)
	s.sendEmail = func(context.Context, string, string, string) error { t.Fatal("duplicate email attempted"); return nil }
	require.NoError(t, s.processNext(ctx))
	job, err = s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, 500, job.Skipped)
	require.Equal(t, "completed", job.Status)
}

func TestTargetedEmailJobInterruptedAttemptIsNotRetried(t *testing.T) {
	s := newTargetedEmailTestService(t)
	ctx := context.Background()
	job, err := s.CreateJob(ctx, []string{"a@example.com", "b@example.com"}, "Subject", "HTML", 5)
	require.NoError(t, err)
	attempt, err := s.reserveNext(ctx)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	_, err = s.db.Exec(`UPDATE targeted_email_job_recipients SET attempted_at = NOW() - INTERVAL '6 minutes' WHERE status = 'sending'`)
	require.NoError(t, err)
	targetedEmailMakeDue(t, s)
	s.sendEmail = func(_ context.Context, email, _, _ string) error {
		require.Equal(t, "b@example.com", email)
		return nil
	}
	require.NoError(t, s.processNext(ctx))
	job, err = s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", job.Status)
	require.Equal(t, 1, job.Failed)
	require.Equal(t, 1, job.Sent)
	require.Contains(t, job.Errors[0], "结果未知")
	claimed, err := s.Claim(ctx, "a@example.com", attempt.hash, "Subject")
	require.NoError(t, err)
	require.False(t, claimed, "uncertain delivery retains its deduplication claim")
}
