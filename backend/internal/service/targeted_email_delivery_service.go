package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

type TargetedEmailDeliveryStatus struct {
	LastSentAt  *time.Time
	SentCurrent bool
	Blocked     bool
}

type TargetedEmailDeliveryService struct {
	db        *sql.DB
	sendEmail func(context.Context, string, string, string) error
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewTargetedEmailDeliveryService(db *sql.DB) *TargetedEmailDeliveryService {
	return &TargetedEmailDeliveryService{db: db, stopCh: make(chan struct{})}
}

func TargetedEmailContentHash(subject, html string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(subject) + "\n" + strings.TrimSpace(html)))
	return hex.EncodeToString(sum[:])
}

func (s *TargetedEmailDeliveryService) Claim(ctx context.Context, recipient, contentHash, subject string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO targeted_email_deliveries (recipient_email, content_hash, subject) VALUES ($1, $2, $3) ON CONFLICT (recipient_email, content_hash) DO NOTHING`, recipient, contentHash, subject)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *TargetedEmailDeliveryService) MarkSent(ctx context.Context, recipient, contentHash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE targeted_email_deliveries SET status = 'sent', sent_at = NOW() WHERE recipient_email = $1 AND content_hash = $2`, recipient, contentHash)
	return err
}

func (s *TargetedEmailDeliveryService) Release(ctx context.Context, recipient, contentHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM targeted_email_deliveries WHERE recipient_email = $1 AND content_hash = $2 AND status = 'sending'`, recipient, contentHash)
	return err
}

func (s *TargetedEmailDeliveryService) Statuses(ctx context.Context, recipients []string, contentHash string) (map[string]TargetedEmailDeliveryStatus, error) {
	result := make(map[string]TargetedEmailDeliveryStatus)
	if len(recipients) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT recipient_email, MAX(sent_at), BOOL_OR(content_hash = $2) FROM targeted_email_deliveries WHERE recipient_email = ANY($1) AND status = 'sent' GROUP BY recipient_email`, pq.Array(recipients), contentHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		var sentAt *time.Time
		var current bool
		if err := rows.Scan(&email, &sentAt, &current); err != nil {
			return nil, err
		}
		result[email] = TargetedEmailDeliveryStatus{LastSentAt: sentAt, SentCurrent: current}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	blockedRows, err := s.db.QueryContext(ctx, `SELECT LOWER(u.email) FROM users u JOIN targeted_email_user_preferences p ON p.user_id = u.id
		WHERE u.deleted_at IS NULL AND p.blocked AND LOWER(u.email) = ANY($1)`, pq.Array(recipients))
	if err != nil {
		return nil, err
	}
	defer blockedRows.Close()
	for blockedRows.Next() {
		var email string
		if err := blockedRows.Scan(&email); err != nil {
			return nil, err
		}
		status := result[email]
		status.Blocked = true
		result[email] = status
	}
	return result, blockedRows.Err()
}
