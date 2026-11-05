CREATE TABLE IF NOT EXISTS targeted_email_jobs (
    id BIGSERIAL PRIMARY KEY,
    subject TEXT NOT NULL,
    html TEXT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 1 AND 1440),
    total INTEGER NOT NULL CHECK (total BETWEEN 1 AND 500),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'cancelled')),
    next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

-- Only one targeted campaign may run at a time, across all server instances.
CREATE UNIQUE INDEX IF NOT EXISTS targeted_email_jobs_one_running_idx
    ON targeted_email_jobs ((1)) WHERE status = 'running';

CREATE TABLE IF NOT EXISTS targeted_email_job_recipients (
    job_id BIGINT NOT NULL REFERENCES targeted_email_jobs(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    recipient_email TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'skipped', 'cancelled')),
    error TEXT NOT NULL DEFAULT '',
    attempted_at TIMESTAMPTZ,
    PRIMARY KEY (job_id, position),
    UNIQUE (job_id, recipient_email)
);

CREATE INDEX IF NOT EXISTS targeted_email_job_recipients_sending_idx
    ON targeted_email_job_recipients (attempted_at, job_id) WHERE status = 'sending';

-- Persist the cooldown across campaigns, restarts and server instances.
CREATE TABLE IF NOT EXISTS targeted_email_queue_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO targeted_email_queue_state (id) VALUES (1) ON CONFLICT DO NOTHING;
