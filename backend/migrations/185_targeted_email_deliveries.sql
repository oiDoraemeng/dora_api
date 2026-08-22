CREATE TABLE IF NOT EXISTS targeted_email_deliveries (
    id BIGSERIAL PRIMARY KEY,
    recipient_email TEXT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    subject TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'sending',
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT targeted_email_deliveries_status_check CHECK (status IN ('sending', 'sent')),
    CONSTRAINT targeted_email_deliveries_recipient_content_key UNIQUE (recipient_email, content_hash)
);

CREATE INDEX IF NOT EXISTS targeted_email_deliveries_recipient_sent_at_idx
    ON targeted_email_deliveries (recipient_email, sent_at DESC);
