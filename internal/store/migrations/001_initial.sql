CREATE TABLE users (
 mid BIGINT NOT NULL DEFAULT 0,
 username TEXT NOT NULL DEFAULT '',
 password TEXT NOT NULL DEFAULT '',
 uname TEXT NOT NULL DEFAULT '',
 face TEXT NOT NULL DEFAULT '',
 sign TEXT NOT NULL DEFAULT '',
 sex TEXT NOT NULL DEFAULT '',
 birthday TEXT NOT NULL DEFAULT '',
 email TEXT NOT NULL DEFAULT '',
 level INTEGER NOT NULL DEFAULT 0,
 exp INTEGER NOT NULL DEFAULT 0,
 coins DOUBLE PRECISION NOT NULL DEFAULT 0,
 role TEXT NOT NULL DEFAULT '',
 status INTEGER NOT NULL DEFAULT 0,
 ctime BIGINT NOT NULL DEFAULT 0,
 top_photo TEXT NOT NULL DEFAULT '',
 vip INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (mid),
 UNIQUE (username)
);

CREATE TABLE sessions (
 position BIGINT NOT NULL,
 token TEXT NOT NULL DEFAULT '',
 csrf TEXT NOT NULL DEFAULT '',
 access TEXT NOT NULL DEFAULT '',
 refresh TEXT NOT NULL DEFAULT '',
 mid BIGINT NOT NULL DEFAULT 0,
 ctime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (token),
 UNIQUE (position),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 UNIQUE (access),
 UNIQUE (refresh)
);

CREATE TABLE regions (
 tid BIGINT NOT NULL DEFAULT 0,
 name TEXT NOT NULL DEFAULT '',
 sort INTEGER NOT NULL DEFAULT 0,
 visible INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (tid)
);

CREATE TABLE archives (
 aid BIGINT NOT NULL DEFAULT 0,
 bvid TEXT NOT NULL DEFAULT '',
 mid BIGINT NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',
 description TEXT NOT NULL DEFAULT '',
 tid BIGINT NOT NULL DEFAULT 0,
 tag TEXT NOT NULL DEFAULT '',
 pic TEXT NOT NULL DEFAULT '',
 ctime BIGINT NOT NULL DEFAULT 0,
 pubdate BIGINT NOT NULL DEFAULT 0,
 state INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT '',
 copyright INTEGER NOT NULL DEFAULT 0,
 source TEXT NOT NULL DEFAULT '',
 chapters TEXT NOT NULL DEFAULT '[]',
 subtitles TEXT NOT NULL DEFAULT '[]',
 stat_view BIGINT NOT NULL DEFAULT 0,
 stat_danmaku BIGINT NOT NULL DEFAULT 0,
 stat_reply BIGINT NOT NULL DEFAULT 0,
 stat_favorite BIGINT NOT NULL DEFAULT 0,
 stat_coin BIGINT NOT NULL DEFAULT 0,
 stat_share BIGINT NOT NULL DEFAULT 0,
 stat_like BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (aid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 UNIQUE (bvid)
);

CREATE TABLE archive_pages (
 cid BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 position INTEGER NOT NULL DEFAULT 0,
 part TEXT NOT NULL DEFAULT '',
 duration INTEGER NOT NULL DEFAULT 0,
 media TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (cid),
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE,
 UNIQUE (aid,position)
);

CREATE TABLE collections (
 id BIGINT NOT NULL DEFAULT 0,
 mid BIGINT NOT NULL DEFAULT 0,
 kind TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 intro TEXT NOT NULL DEFAULT '',
 cover TEXT NOT NULL DEFAULT '',
 ctime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (id),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE collection_items (
 collection_id BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 position INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (collection_id,aid),
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE,
 FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE,
 UNIQUE (collection_id,position)
);

CREATE TABLE seasons (
 season_id BIGINT NOT NULL DEFAULT 0,
 media_id BIGINT NOT NULL DEFAULT 0,
 season_type INTEGER NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',
 cover TEXT NOT NULL DEFAULT '',
 horizontal TEXT NOT NULL DEFAULT '',
 evaluate TEXT NOT NULL DEFAULT '',
 areas TEXT NOT NULL DEFAULT '[]',
 styles TEXT NOT NULL DEFAULT '[]',
 score DOUBLE PRECISION NOT NULL DEFAULT 0,
 is_finish INTEGER NOT NULL DEFAULT 0,
 weekday INTEGER NOT NULL DEFAULT 0,
 pub_time TEXT NOT NULL DEFAULT '',
 views BIGINT NOT NULL DEFAULT 0,
 follow BIGINT NOT NULL DEFAULT 0,
 danmakus BIGINT NOT NULL DEFAULT 0,
 ctime BIGINT NOT NULL DEFAULT 0,
 state INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (season_id),
 UNIQUE (media_id)
);

CREATE TABLE episodes (
 ep_id BIGINT NOT NULL DEFAULT 0,
 season_id BIGINT NOT NULL DEFAULT 0,
 position INTEGER NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 cid BIGINT NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',
 long_title TEXT NOT NULL DEFAULT '',
 cover TEXT NOT NULL DEFAULT '',
 duration INTEGER NOT NULL DEFAULT 0,
 pub_time BIGINT NOT NULL DEFAULT 0,
 media TEXT NOT NULL DEFAULT '',
 badge TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (ep_id),
 FOREIGN KEY (season_id) REFERENCES seasons(season_id) ON DELETE CASCADE,
 UNIQUE (aid),
 UNIQUE (cid),
 UNIQUE (season_id,position)
);

CREATE TABLE live_areas (
 id BIGINT NOT NULL DEFAULT 0,
 name TEXT NOT NULL DEFAULT '',
 position INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (id)
);

CREATE TABLE live_area_children (
 id BIGINT NOT NULL DEFAULT 0,
 parent_id BIGINT NOT NULL DEFAULT 0,
 name TEXT NOT NULL DEFAULT '',
 position INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (id),
 FOREIGN KEY (parent_id) REFERENCES live_areas(id) ON DELETE CASCADE
);

CREATE TABLE rooms (
 room_id BIGINT NOT NULL DEFAULT 0,
 uid BIGINT NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',
 cover TEXT NOT NULL DEFAULT '',
 parent_area_id BIGINT NOT NULL DEFAULT 0,
 area_id BIGINT NOT NULL DEFAULT 0,
 live_status INTEGER NOT NULL DEFAULT 0,
 online BIGINT NOT NULL DEFAULT 0,
 media TEXT NOT NULL DEFAULT '',
 banned INTEGER NOT NULL DEFAULT 0,
 live_time BIGINT NOT NULL DEFAULT 0,
 tags TEXT NOT NULL DEFAULT '',
 description TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT 'null',
 PRIMARY KEY (room_id),
 FOREIGN KEY (uid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (parent_area_id) REFERENCES live_areas(id) ON DELETE RESTRICT,
 FOREIGN KEY (area_id) REFERENCES live_area_children(id) ON DELETE RESTRICT
);

CREATE TABLE live_messages (
 id BIGINT NOT NULL DEFAULT 0,
 room_id BIGINT NOT NULL DEFAULT 0,
 uid BIGINT NOT NULL DEFAULT 0,
 text TEXT NOT NULL DEFAULT '',
 ts BIGINT NOT NULL DEFAULT 0,
 price DOUBLE PRECISION,
 PRIMARY KEY (id),
 FOREIGN KEY (room_id) REFERENCES rooms(room_id) ON DELETE CASCADE,
 FOREIGN KEY (uid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE replies (
 rpid BIGINT NOT NULL DEFAULT 0,
 oid BIGINT NOT NULL DEFAULT 0,
 mid BIGINT NOT NULL DEFAULT 0,
 root BIGINT,
 parent BIGINT,
 message TEXT NOT NULL DEFAULT '',
 ctime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (rpid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (oid) REFERENCES archives(aid) ON DELETE CASCADE,
 FOREIGN KEY (root) REFERENCES replies(rpid) ON DELETE CASCADE,
 FOREIGN KEY (parent) REFERENCES replies(rpid) ON DELETE SET NULL
);

CREATE TABLE reply_reactions (
 rpid BIGINT NOT NULL DEFAULT 0,
 mid BIGINT NOT NULL DEFAULT 0,
 reaction INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (rpid,mid),
 FOREIGN KEY (rpid) REFERENCES replies(rpid) ON DELETE CASCADE,
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 CHECK (reaction IN (1,2))
);

CREATE TABLE danmaku (
 id BIGINT NOT NULL DEFAULT 0,
 cid BIGINT NOT NULL DEFAULT 0,
 progress INTEGER NOT NULL DEFAULT 0,
 mode INTEGER NOT NULL DEFAULT 0,
 fontsize INTEGER NOT NULL DEFAULT 0,
 color INTEGER NOT NULL DEFAULT 0,
 mid BIGINT NOT NULL DEFAULT 0,
 content TEXT NOT NULL DEFAULT '',
 ctime BIGINT NOT NULL DEFAULT 0,
 weight INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (id),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE seeded_danmaku (
 cid BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (cid)
);

CREATE TABLE relations (
 mid BIGINT NOT NULL DEFAULT 0,
 fid BIGINT NOT NULL DEFAULT 0,
 mtime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,fid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (fid) REFERENCES users(mid) ON DELETE CASCADE,
 CHECK (mid <> fid)
);

CREATE TABLE likes (
 mid BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,aid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE
);

CREATE TABLE dislikes (
 mid BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,aid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE
);

CREATE TABLE coins (
 mid BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 n INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,aid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE
);

CREATE TABLE fav_folders (
 id BIGINT NOT NULL DEFAULT 0,
 mid BIGINT NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',
 intro TEXT NOT NULL DEFAULT '',
 privacy INTEGER NOT NULL DEFAULT 0,
 ctime BIGINT NOT NULL DEFAULT 0,
 attr INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (id),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE fav_items (
 folder_id BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 mtime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (folder_id,aid),
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE,
 FOREIGN KEY (folder_id) REFERENCES fav_folders(id) ON DELETE CASCADE
);

CREATE TABLE history (
 mid BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 cid BIGINT NOT NULL DEFAULT 0,
 epid BIGINT,
 sid BIGINT,
 view_at BIGINT NOT NULL DEFAULT 0,
 progress INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,aid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (epid) REFERENCES episodes(ep_id) ON DELETE SET NULL,
 FOREIGN KEY (sid) REFERENCES seasons(season_id) ON DELETE SET NULL
);

CREATE TABLE history_paused (
 mid BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (mid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE toview (
 mid BIGINT NOT NULL DEFAULT 0,
 aid BIGINT NOT NULL DEFAULT 0,
 add_at BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,aid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (aid) REFERENCES archives(aid) ON DELETE CASCADE
);

CREATE TABLE bangumi_follow (
 mid BIGINT NOT NULL DEFAULT 0,
 season_id BIGINT NOT NULL DEFAULT 0,
 status INTEGER NOT NULL DEFAULT 0,
 mtime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (mid,season_id),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE,
 FOREIGN KEY (season_id) REFERENCES seasons(season_id) ON DELETE CASCADE
);

CREATE TABLE dm_config (
 mid BIGINT NOT NULL DEFAULT 0,
 values_json TEXT NOT NULL DEFAULT '{}',
 PRIMARY KEY (mid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE dm_filter (
 mid BIGINT NOT NULL DEFAULT 0,
 rule TEXT NOT NULL DEFAULT '[]',
 keyword TEXT NOT NULL DEFAULT '[]',
 regex TEXT NOT NULL DEFAULT '[]',
 PRIMARY KEY (mid),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE CASCADE
);

CREATE TABLE banners (
 id BIGINT NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',
 img TEXT NOT NULL DEFAULT '',
 url TEXT NOT NULL DEFAULT '',
 position TEXT NOT NULL DEFAULT '',
 sort INTEGER NOT NULL DEFAULT 0,
 season_id BIGINT,
 PRIMARY KEY (id),
 FOREIGN KEY (season_id) REFERENCES seasons(season_id) ON DELETE CASCADE
);

CREATE TABLE settings (
 key TEXT NOT NULL DEFAULT '',
 value TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (key)
);

CREATE TABLE logs (
 id BIGINT NOT NULL DEFAULT 0,
 mid BIGINT,
 action TEXT NOT NULL DEFAULT '',
 target TEXT NOT NULL DEFAULT '',
 ctime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (id),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE SET NULL
);

CREATE TABLE qrcodes (
 code TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT '',
 mid BIGINT,
 ctime BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (code),
 FOREIGN KEY (mid) REFERENCES users(mid) ON DELETE SET NULL
);

CREATE TABLE ids (
 name TEXT NOT NULL DEFAULT '',
 value BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (name)
);

CREATE INDEX idx_sessions_mid ON sessions (mid);

CREATE INDEX idx_sessions_ctime ON sessions (ctime);

CREATE INDEX idx_archives_mid_state_pubdate ON archives (mid,state,pubdate);

CREATE INDEX idx_archives_state_pubdate ON archives (state,pubdate);

CREATE INDEX idx_archives_tid_state ON archives (tid,state);

CREATE INDEX idx_archives_stat_view ON archives (stat_view);

CREATE INDEX idx_collections_mid_kind ON collections (mid,kind);

CREATE INDEX idx_collection_items_aid ON collection_items (aid);

CREATE INDEX idx_seasons_season_type_state ON seasons (season_type,state);

CREATE INDEX idx_seasons_state_views ON seasons (state,views);

CREATE INDEX idx_episodes_season_id ON episodes (season_id);

CREATE INDEX idx_live_area_children_parent_id ON live_area_children (parent_id);

CREATE INDEX idx_rooms_uid ON rooms (uid);

CREATE INDEX idx_rooms_parent_area_id_area_id_live_status ON rooms (parent_area_id,area_id,live_status);

CREATE INDEX idx_rooms_live_status_online ON rooms (live_status,online);

CREATE INDEX idx_live_messages_room_id_ts ON live_messages (room_id,ts);

CREATE INDEX idx_live_messages_uid ON live_messages (uid);

CREATE INDEX idx_replies_oid_root_ctime ON replies (oid,root,ctime);

CREATE INDEX idx_replies_root_ctime ON replies (root,ctime);

CREATE INDEX idx_replies_parent ON replies (parent);

CREATE INDEX idx_replies_mid ON replies (mid);

CREATE INDEX idx_reply_reactions_mid ON reply_reactions (mid);

CREATE INDEX idx_danmaku_cid_progress ON danmaku (cid,progress);

CREATE INDEX idx_danmaku_mid ON danmaku (mid);

CREATE INDEX idx_relations_fid ON relations (fid);

CREATE INDEX idx_likes_aid ON likes (aid);

CREATE INDEX idx_dislikes_aid ON dislikes (aid);

CREATE INDEX idx_coins_aid ON coins (aid);

CREATE INDEX idx_fav_folders_mid ON fav_folders (mid);

CREATE INDEX idx_fav_items_aid ON fav_items (aid);

CREATE INDEX idx_fav_items_folder_id_mtime ON fav_items (folder_id,mtime);

CREATE INDEX idx_history_mid_view_at ON history (mid,view_at);

CREATE INDEX idx_history_cid ON history (cid);

CREATE INDEX idx_history_epid ON history (epid);

CREATE INDEX idx_history_sid ON history (sid);

CREATE INDEX idx_toview_mid_add_at ON toview (mid,add_at);

CREATE INDEX idx_toview_aid ON toview (aid);

CREATE INDEX idx_bangumi_follow_season_id ON bangumi_follow (season_id);

CREATE INDEX idx_bangumi_follow_mid_mtime ON bangumi_follow (mid,mtime);

CREATE INDEX idx_banners_position_sort ON banners (position,sort);

CREATE INDEX idx_banners_season_id ON banners (season_id);

CREATE INDEX idx_logs_ctime ON logs (ctime);

CREATE INDEX idx_logs_mid ON logs (mid);

CREATE INDEX idx_qrcodes_ctime ON qrcodes (ctime);

CREATE INDEX idx_qrcodes_mid ON qrcodes (mid);

INSERT INTO ids (name,value) VALUES ('global',1000);

INSERT INTO ids (name,value) VALUES ('sessions',0);
