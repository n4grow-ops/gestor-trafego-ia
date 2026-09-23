ALTER TABLE outbox_jobs ADD COLUMN max_attempts int NOT NULL DEFAULT 3 CHECK (max_attempts > 0);
