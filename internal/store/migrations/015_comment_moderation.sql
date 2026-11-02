CREATE TABLE IF NOT EXISTS comment_reports (
    mid BIGINT NOT NULL REFERENCES users(mid) ON DELETE CASCADE,
    rpid BIGINT NOT NULL,
    reason TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    ctime BIGINT NOT NULL,
    PRIMARY KEY (mid, rpid)
);
CREATE INDEX IF NOT EXISTS comment_reports_pending ON comment_reports(status, rpid);
CREATE TABLE IF NOT EXISTS user_blocks (
    mid BIGINT NOT NULL REFERENCES users(mid) ON DELETE CASCADE,
    fid BIGINT NOT NULL REFERENCES users(mid) ON DELETE CASCADE,
    ctime BIGINT NOT NULL,
    PRIMARY KEY (mid, fid),
    CHECK (mid <> fid)
);
