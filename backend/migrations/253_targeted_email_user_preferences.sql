CREATE TABLE IF NOT EXISTS targeted_email_user_preferences (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    blocked BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Keep the campaign bound to the selected user even if their email changes.
ALTER TABLE targeted_email_job_recipients
    ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

UPDATE targeted_email_job_recipients r
SET user_id = u.id
FROM users u
WHERE r.user_id IS NULL AND r.status IN ('pending', 'sending')
    AND u.deleted_at IS NULL AND LOWER(u.email) = r.recipient_email;
