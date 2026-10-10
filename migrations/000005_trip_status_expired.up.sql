-- Past trips with no approved booking are marked expired by the maintenance job.
-- PostgreSQL 12+ allows ADD VALUE inside the migration transaction; the new
-- value is not used in this file.

ALTER TYPE trip_status ADD VALUE IF NOT EXISTS 'expired';
