ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_status_known;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_reason_known;

DROP TABLE IF EXISTS notifications;
