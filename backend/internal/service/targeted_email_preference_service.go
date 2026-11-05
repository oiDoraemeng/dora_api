package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrTargetedEmailBlocked = infraerrors.BadRequest("TARGETED_EMAIL_BLOCKED", "该用户已禁止接收群发邮件")

// SetUserBlocked persists a user-level preference for all targeted templates.
// Serialize with reservation so a saved ban applies to every unreserved email.
func (s *TargetedEmailDeliveryService) SetUserBlocked(ctx context.Context, userID int64, blocked bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stateID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM targeted_email_queue_state WHERE id = 1 FOR UPDATE`).Scan(&stateID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO targeted_email_user_preferences (user_id, blocked)
		SELECT id, $2 FROM users WHERE id = $1 AND deleted_at IS NULL
		ON CONFLICT (user_id) DO UPDATE SET blocked = EXCLUDED.blocked, updated_at = NOW()`, userID, blocked)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
	}
	return tx.Commit()
}

const targetedEmailRecipientBlockedSQL = `SELECT EXISTS (
	SELECT 1 FROM targeted_email_user_preferences p JOIN users u ON u.id = p.user_id
	WHERE p.blocked AND (p.user_id = $1 OR (u.deleted_at IS NULL AND LOWER(u.email) = $2))
)`

func (s *TargetedEmailDeliveryService) IsRecipientBlocked(ctx context.Context, recipient string) (bool, error) {
	var blocked bool
	err := s.db.QueryRowContext(ctx, targetedEmailRecipientBlockedSQL, nil, strings.ToLower(strings.TrimSpace(recipient))).Scan(&blocked)
	return blocked, err
}
