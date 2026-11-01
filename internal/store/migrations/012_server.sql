INSERT INTO settings(key,value) VALUES ('serverPublicOrigin','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serverEmbyUrl','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serverEmbyUsername','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serverEmbyPassword','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serverFfmpeg','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serverFfprobe','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serverVideoEncoder','""') ON CONFLICT(key) DO NOTHING;
