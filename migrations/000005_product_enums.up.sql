-- New enum values for the trip and booking lifecycles.
-- Kept in their own migration: a value added by ALTER TYPE ... ADD VALUE cannot be
-- used in the same transaction, and 000006 uses these values in constraints and indexes.

ALTER TYPE trip_status ADD VALUE IF NOT EXISTS 'in_progress';
ALTER TYPE trip_status ADD VALUE IF NOT EXISTS 'expired';

ALTER TYPE booking_status ADD VALUE IF NOT EXISTS 'completed';
ALTER TYPE booking_status ADD VALUE IF NOT EXISTS 'expired';

ALTER TYPE location_type ADD VALUE IF NOT EXISTS 'settlement';
