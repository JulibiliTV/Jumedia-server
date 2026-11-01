CREATE TABLE data_sources (
 id BIGINT PRIMARY KEY, mid BIGINT NOT NULL REFERENCES users(mid) ON DELETE CASCADE,
 system INTEGER NOT NULL DEFAULT 0, type TEXT NOT NULL DEFAULT 'emby', name TEXT NOT NULL,
 url TEXT NOT NULL, username TEXT NOT NULL, password_enc TEXT NOT NULL DEFAULT '',
 server_name TEXT NOT NULL DEFAULT '', server_id TEXT NOT NULL DEFAULT '', libraries TEXT NOT NULL DEFAULT '[]',
 auto_sync INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'idle', message TEXT NOT NULL DEFAULT '',
 last_sync BIGINT NOT NULL DEFAULT 0, ctime BIGINT NOT NULL
);
CREATE UNIQUE INDEX data_sources_account ON data_sources(mid,system,server_id,username);
ALTER TABLE archives ADD COLUMN origin_ds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE archives ADD COLUMN origin_item TEXT NOT NULL DEFAULT '';
ALTER TABLE archives ADD COLUMN origin TEXT NOT NULL DEFAULT 'null';
CREATE UNIQUE INDEX archives_origin ON archives(origin_ds,origin_item) WHERE origin_ds<>0;
ALTER TABLE collections ADD COLUMN origin_ds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE collections ADD COLUMN origin_library TEXT NOT NULL DEFAULT '';
ALTER TABLE collections ADD COLUMN origin TEXT NOT NULL DEFAULT 'null';
CREATE UNIQUE INDEX collections_origin ON collections(origin_ds,origin_library) WHERE origin_ds<>0;
INSERT INTO settings(key,value) VALUES ('userDataSources','true') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('dataSourcePrivateNetwork','false') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('dataSourceSyncHours','6') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('officialMid','1') ON CONFLICT(key) DO NOTHING;
