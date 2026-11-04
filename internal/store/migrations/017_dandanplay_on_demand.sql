CREATE TABLE dandanplay_sync_next (
 cid BIGINT PRIMARY KEY,
 aid BIGINT NOT NULL,
 archive_aid BIGINT REFERENCES archives(aid) ON DELETE CASCADE,
 source_ep_id BIGINT REFERENCES episodes(ep_id) ON DELETE CASCADE,
 media TEXT NOT NULL,
 filename TEXT NOT NULL,
 duration INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',
 message TEXT NOT NULL DEFAULT '',
 episode_id BIGINT NOT NULL DEFAULT 0,
 anime_title TEXT NOT NULL DEFAULT '',
 episode_title TEXT NOT NULL DEFAULT '',
 count INTEGER NOT NULL DEFAULT 0,
 updated_at BIGINT NOT NULL DEFAULT 0,
 requested_at BIGINT NOT NULL DEFAULT 0,
 attempted_at BIGINT NOT NULL DEFAULT 0
);
INSERT INTO dandanplay_sync_next(cid,aid,archive_aid,media,filename,duration,status,message,episode_id,anime_title,episode_title,count,updated_at)
 SELECT cid,aid,aid,media,filename,duration,status,message,episode_id,anime_title,episode_title,count,updated_at FROM dandanplay_sync;
CREATE TABLE dandanplay_comments_next (
 id BIGINT PRIMARY KEY,
 cid BIGINT NOT NULL REFERENCES dandanplay_sync_next(cid) ON DELETE CASCADE,
 progress INTEGER NOT NULL,
 mode INTEGER NOT NULL,
 fontsize INTEGER NOT NULL,
 color INTEGER NOT NULL,
 content TEXT NOT NULL,
 ctime BIGINT NOT NULL,
 weight INTEGER NOT NULL
);
INSERT INTO dandanplay_comments_next SELECT * FROM dandanplay_comments;
DROP TABLE dandanplay_comments;
DROP TABLE dandanplay_sync;
ALTER TABLE dandanplay_sync_next RENAME TO dandanplay_sync;
ALTER TABLE dandanplay_comments_next RENAME TO dandanplay_comments;
CREATE INDEX idx_dandanplay_sync_status ON dandanplay_sync(status,requested_at,cid);
CREATE INDEX idx_dandanplay_comments_cid ON dandanplay_comments(cid,progress);
