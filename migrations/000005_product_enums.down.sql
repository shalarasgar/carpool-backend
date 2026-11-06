-- PostgreSQL cannot drop a single enum value. The values added by the up migration
-- are unused once 000006 is rolled back, so this down migration is a no-op.
SELECT 1;
