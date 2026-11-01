INSERT INTO settings(key,value) VALUES ('dataSourceSyncRate','5') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('dataSourceSyncConcurrency','2') ON CONFLICT(key) DO NOTHING;
