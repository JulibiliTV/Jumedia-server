CREATE TABLE live_caches (
 key TEXT PRIMARY KEY,
 media TEXT NOT NULL,
 height INTEGER NOT NULL,
 aid BIGINT NOT NULL,
 cid BIGINT NOT NULL,
 duration DOUBLE PRECISION NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','ready','failed')),
 progress INTEGER NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 100),
 message TEXT NOT NULL DEFAULT '',
 size BIGINT NOT NULL DEFAULT 0,
 dir TEXT NOT NULL,
 ctime BIGINT NOT NULL,
 mtime BIGINT NOT NULL,
 started BIGINT NOT NULL DEFAULT 0,
 unused_since BIGINT NOT NULL DEFAULT 0,
 UNIQUE(media,height)
);
ALTER TABLE rooms ADD COLUMN pending_start INTEGER NOT NULL DEFAULT 0;
INSERT INTO settings(key,value) VALUES ('liveCacheDir','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('liveTranscodeHeight','1080') ON CONFLICT(key) DO NOTHING;
