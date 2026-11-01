-- Mock v18: count every reply, including children, and reset empty archives to zero.
UPDATE archives
SET stat_reply = (SELECT COUNT(*) FROM replies WHERE replies.oid = archives.aid);
