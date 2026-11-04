CREATE TABLE dandanplay_sync (
 cid BIGINT PRIMARY KEY,
 aid BIGINT NOT NULL REFERENCES archives(aid) ON DELETE CASCADE,
 media TEXT NOT NULL,
 filename TEXT NOT NULL,
 duration INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',
 message TEXT NOT NULL DEFAULT '',
 episode_id BIGINT NOT NULL DEFAULT 0,
 anime_title TEXT NOT NULL DEFAULT '',
 episode_title TEXT NOT NULL DEFAULT '',
 count INTEGER NOT NULL DEFAULT 0,
 updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_dandanplay_sync_status ON dandanplay_sync(status,cid);
CREATE TABLE dandanplay_comments (
 id BIGINT PRIMARY KEY,
 cid BIGINT NOT NULL REFERENCES dandanplay_sync(cid) ON DELETE CASCADE,
 progress INTEGER NOT NULL,
 mode INTEGER NOT NULL,
 fontsize INTEGER NOT NULL,
 color INTEGER NOT NULL,
 content TEXT NOT NULL,
 ctime BIGINT NOT NULL,
 weight INTEGER NOT NULL
);
CREATE INDEX idx_dandanplay_comments_cid ON dandanplay_comments(cid,progress);
