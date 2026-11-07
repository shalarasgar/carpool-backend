DROP INDEX IF EXISTS notifications_unpushed_idx;
ALTER TABLE notifications DROP COLUMN IF EXISTS pushed_at;
DROP TABLE IF EXISTS push_tokens;
