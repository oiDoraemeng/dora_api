package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"
)

const DefaultTargetedEmailIntervalMinutes = 5

var ErrTargetedEmailJobRunning = infraerrors.Conflict("TARGETED_EMAIL_JOB_RUNNING", "已有邮件发送任务正在进行，请等待完成或取消后再创建")

type TargetedEmailJob struct {
	ID              int64      `json:"id"`
	Subject         string     `json:"subject"`
	IntervalMinutes int        `json:"interval_minutes"`
	Total           int        `json:"total"`
	Status          string     `json:"status"`
	Sent            int        `json:"sent"`
	Failed          int        `json:"failed"`
	Skipped         int        `json:"skipped"`
	Pending         int        `json:"pending"`
	Sending         int        `json:"sending"`
	Cancelled       int        `json:"cancelled"`
	NextSendAt      time.Time  `json:"next_send_at"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Errors          []string   `json:"errors"`
}

func (s *TargetedEmailDeliveryService) Start(email *EmailService) {
	if s == nil || s.db == nil || email == nil {
		return
	}
	s.startOnce.Do(func() {
		s.sendEmail = email.SendEmail
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.stopCh:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if err := s.processNext(ctx); err != nil {
					slog.Error("targeted email worker failed", "error", err)
				}
				cancel()
				select {
				case <-s.stopCh:
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func (s *TargetedEmailDeliveryService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *TargetedEmailDeliveryService) CreateJob(ctx context.Context, recipients []string, subject, html string, interval int) (*TargetedEmailJob, error) {
	if interval < 1 || interval > 1440 || len(recipients) < 1 || len(recipients) > 500 {
		return nil, infraerrors.BadRequest("INVALID_TARGETED_EMAIL_JOB", "发送间隔须为 1–1440 分钟，收件人须为 1–500 人")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO targeted_email_jobs (subject, html, content_hash, interval_minutes, total, next_send_at)
		VALUES ($1, $2, $3, $4, $5, GREATEST(NOW(), (SELECT next_send_at FROM targeted_email_queue_state WHERE id = 1))) RETURNING id`,
		subject, html, TargetedEmailContentHash(subject, html), interval, len(recipients)).Scan(&id)
	if err != nil {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Constraint == "targeted_email_jobs_one_running_idx" {
			return nil, ErrTargetedEmailJobRunning
		}
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO targeted_email_job_recipients (job_id, position, recipient_email, user_id)
		SELECT $1, ordinal::integer, email,
			(SELECT id FROM users u WHERE u.deleted_at IS NULL AND LOWER(u.email) = r.email ORDER BY id LIMIT 1)
		FROM unnest($2::text[]) WITH ORDINALITY AS r(email, ordinal)`, id, pq.Array(recipients))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// GetJob with id=0 returns the latest campaign, including after a page reload.
func (s *TargetedEmailDeliveryService) GetJob(ctx context.Context, id int64) (*TargetedEmailJob, error) {
	var job TargetedEmailJob
	err := s.db.QueryRowContext(ctx, `SELECT j.id, j.subject, j.interval_minutes, j.total, j.status, j.next_send_at, j.created_at, j.completed_at,
		COUNT(*) FILTER (WHERE r.status = 'sent'), COUNT(*) FILTER (WHERE r.status = 'failed'), COUNT(*) FILTER (WHERE r.status = 'skipped'),
		COUNT(*) FILTER (WHERE r.status = 'pending'), COUNT(*) FILTER (WHERE r.status = 'sending'), COUNT(*) FILTER (WHERE r.status = 'cancelled')
		FROM targeted_email_jobs j JOIN targeted_email_job_recipients r ON r.job_id = j.id
		WHERE j.id = CASE WHEN $1::bigint = 0 THEN (SELECT MAX(id) FROM targeted_email_jobs) ELSE $1 END GROUP BY j.id`, id).
		Scan(&job.ID, &job.Subject, &job.IntervalMinutes, &job.Total, &job.Status, &job.NextSendAt, &job.CreatedAt, &job.CompletedAt,
			&job.Sent, &job.Failed, &job.Skipped, &job.Pending, &job.Sending, &job.Cancelled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.Errors = []string{}
	rows, err := s.db.QueryContext(ctx, `SELECT recipient_email, error FROM targeted_email_job_recipients WHERE job_id = $1 AND error <> '' ORDER BY position LIMIT 20`, job.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var email, message string
		if err := rows.Scan(&email, &message); err != nil {
			return nil, err
		}
		job.Errors = append(job.Errors, email+": "+message)
	}
	return &job, rows.Err()
}

func (s *TargetedEmailDeliveryService) CancelJob(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the job before its recipients, in the same order as the worker.
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_jobs SET status = 'cancelled', completed_at = NOW() WHERE id = $1 AND status = 'running'`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_job_recipients SET status = 'cancelled' WHERE job_id = $1 AND status = 'pending'`, id); err != nil {
		return err
	}
	return tx.Commit()
}

type targetedEmailAttempt struct {
	jobID                      int64
	position                   int
	userID                     sql.NullInt64
	email, subject, html, hash string
}

// reserveNext serializes scheduling through a durable global row lock. The claim
// commits before SMTP: a crash must never silently retry an uncertain delivery.
func (s *TargetedEmailDeliveryService) reserveNext(ctx context.Context) (*targetedEmailAttempt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var due bool
	err = tx.QueryRowContext(ctx, `SELECT next_send_at <= NOW() FROM targeted_email_queue_state WHERE id = 1 FOR UPDATE SKIP LOCKED`).Scan(&due)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// An interrupted SMTP attempt has an unknown outcome. Preserve its dedup
	// claim and expose the uncertainty instead of potentially sending twice.
	if _, err := tx.ExecContext(ctx, `WITH locked_jobs AS MATERIALIZED (
		SELECT id FROM targeted_email_jobs j WHERE EXISTS (SELECT 1 FROM targeted_email_job_recipients r WHERE r.job_id = j.id AND r.status = 'sending' AND r.attempted_at < NOW() - INTERVAL '5 minutes') FOR UPDATE
	) UPDATE targeted_email_job_recipients SET status = 'failed', error = '发送中断，结果未知；为避免重复发送未自动重试'
		WHERE job_id IN (SELECT id FROM locked_jobs) AND status = 'sending' AND attempted_at < NOW() - INTERVAL '5 minutes'`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, completeTargetedEmailJobsSQL); err != nil {
		return nil, err
	}
	if !due {
		return nil, tx.Commit()
	}
	var sending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM targeted_email_job_recipients WHERE status = 'sending')`).Scan(&sending); err != nil {
		return nil, err
	}
	if sending {
		return nil, tx.Commit()
	}
	a := &targetedEmailAttempt{}
	var interval int
	err = tx.QueryRowContext(ctx, `SELECT id, subject, html, content_hash, interval_minutes FROM targeted_email_jobs WHERE status = 'running' ORDER BY id LIMIT 1 FOR UPDATE`).
		Scan(&a.jobID, &a.subject, &a.html, &a.hash, &interval)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	// Skip previously claimed content without consuming a sending slot.
	for {
		err = tx.QueryRowContext(ctx, `SELECT position, recipient_email, user_id FROM targeted_email_job_recipients WHERE job_id = $1 AND status = 'pending' ORDER BY position LIMIT 1 FOR UPDATE`, a.jobID).
			Scan(&a.position, &a.email, &a.userID)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, completeTargetedEmailJobsSQL); err != nil {
				return nil, err
			}
			return nil, tx.Commit()
		}
		if err != nil {
			return nil, err
		}
		var blocked bool
		if err := tx.QueryRowContext(ctx, targetedEmailRecipientBlockedSQL, a.userID, a.email).Scan(&blocked); err != nil {
			return nil, err
		}
		if blocked {
			if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_job_recipients SET status = 'skipped', error = '用户已禁止接收群发邮件' WHERE job_id = $1 AND position = $2`, a.jobID, a.position); err != nil {
				return nil, err
			}
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO targeted_email_deliveries (recipient_email, content_hash, subject) VALUES ($1, $2, $3) ON CONFLICT (recipient_email, content_hash) DO NOTHING`, a.email, a.hash, a.subject)
		if err != nil {
			return nil, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if count == 1 {
			break
		}
		if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_job_recipients SET status = 'skipped' WHERE job_id = $1 AND position = $2`, a.jobID, a.position); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_job_recipients SET status = 'sending', attempted_at = NOW() WHERE job_id = $1 AND position = $2`, a.jobID, a.position); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_queue_state SET next_send_at = NOW() + $1 * INTERVAL '1 minute' WHERE id = 1`, interval); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_jobs SET next_send_at = (SELECT next_send_at FROM targeted_email_queue_state WHERE id = 1) WHERE id = $1`, a.jobID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a, nil
}

const completeTargetedEmailJobsSQL = `UPDATE targeted_email_jobs j SET status = 'completed', completed_at = NOW()
	WHERE status = 'running' AND NOT EXISTS (SELECT 1 FROM targeted_email_job_recipients r WHERE r.job_id = j.id AND r.status IN ('pending', 'sending'))`

func (s *TargetedEmailDeliveryService) processNext(ctx context.Context) error {
	attempt, err := s.reserveNext(ctx)
	if err != nil || attempt == nil {
		return err
	}
	sendErr := s.sendEmail(ctx, attempt.email, attempt.subject, attempt.html)
	// Use a fresh context so a request/shutdown deadline cannot discard an SMTP result.
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.finishAttempt(finishCtx, attempt, sendErr)
}

func (s *TargetedEmailDeliveryService) finishAttempt(ctx context.Context, a *targetedEmailAttempt, sendErr error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Match scheduling lock order: global state, then job, then recipient.
	var stateID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM targeted_email_queue_state WHERE id = 1 FOR UPDATE`).Scan(&stateID); err != nil {
		return err
	}
	var jobID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM targeted_email_jobs WHERE id = $1 FOR UPDATE`, a.jobID).Scan(&jobID); err != nil {
		return err
	}
	status, message := "sent", ""
	if sendErr != nil {
		status, message = "failed", sendErr.Error()
		_, err = tx.ExecContext(ctx, `DELETE FROM targeted_email_deliveries WHERE recipient_email = $1 AND content_hash = $2 AND status = 'sending'`, a.email, a.hash)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE targeted_email_deliveries SET status = 'sent', sent_at = NOW() WHERE recipient_email = $1 AND content_hash = $2`, a.email, a.hash)
	}
	if err != nil {
		return fmt.Errorf("record targeted email outcome: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_job_recipients SET status = $3, error = $4 WHERE job_id = $1 AND position = $2`, a.jobID, a.position, status, message); err != nil {
		return err
	}
	// Measure cooldown from completion, including failed attempts.
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_queue_state SET next_send_at = GREATEST(next_send_at, NOW() + (SELECT interval_minutes FROM targeted_email_jobs WHERE id = $1) * INTERVAL '1 minute') WHERE id = 1`, a.jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE targeted_email_jobs SET next_send_at = (SELECT next_send_at FROM targeted_email_queue_state WHERE id = 1) WHERE status = 'running'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, completeTargetedEmailJobsSQL); err != nil {
		return err
	}
	return tx.Commit()
}
