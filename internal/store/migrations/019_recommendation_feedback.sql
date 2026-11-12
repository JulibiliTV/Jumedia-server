CREATE TABLE IF NOT EXISTS recommendation_feedback (
    mid BIGINT NOT NULL REFERENCES users(mid) ON DELETE CASCADE,
    reason_id INTEGER NOT NULL CHECK (reason_id IN (1, 4)),
    target_id BIGINT NOT NULL CHECK (target_id > 0),
    ctime BIGINT NOT NULL,
    PRIMARY KEY (mid, reason_id, target_id)
);
