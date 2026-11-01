ALTER TABLE regions ADD COLUMN parent BIGINT;
ALTER TABLE archives ADD COLUMN sub_tid BIGINT NOT NULL DEFAULT 0;
ALTER TABLE seasons ADD COLUMN copyright INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notice TEXT NOT NULL DEFAULT '';
ALTER TABLE banners ADD COLUMN loc BIGINT;
CREATE TABLE reservations (
 sid BIGINT PRIMARY KEY,
 mid BIGINT NOT NULL REFERENCES users(mid) ON DELETE CASCADE,
 title TEXT NOT NULL,
 stime BIGINT NOT NULL,
 ctime BIGINT NOT NULL
);
CREATE INDEX idx_regions_parent_sort ON regions(parent,sort);
CREATE INDEX idx_archives_sub_tid ON archives(sub_tid);
CREATE INDEX idx_banners_channel ON banners(position,loc,sort);
CREATE INDEX idx_reservations_mid_stime ON reservations(mid,stime);
