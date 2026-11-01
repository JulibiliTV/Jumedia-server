INSERT INTO settings(key,value) VALUES ('serviceApi','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serviceLive','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serviceApp','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('servicePassport','""') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serviceCdns','"[]"') ON CONFLICT(key) DO NOTHING;
INSERT INTO settings(key,value) VALUES ('serviceMtime','0') ON CONFLICT(key) DO NOTHING;
