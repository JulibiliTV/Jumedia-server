INSERT INTO settings(key,value) VALUES ('imageCacheEnabled','true') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('imageCacheDir','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('imageCacheMaxMB','1024') ON CONFLICT(key) DO NOTHING;
