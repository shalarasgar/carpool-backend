DROP TABLE IF EXISTS notifications;

DROP INDEX IF EXISTS reviews_to_user_created_idx;
ALTER TABLE reviews
    DROP CONSTRAINT IF EXISTS reviews_comment_length,
    DROP CONSTRAINT IF EXISTS reviews_booking_author_key,
    DROP COLUMN IF EXISTS booking_id;

DROP INDEX IF EXISTS bookings_passenger_created_idx;
DROP INDEX IF EXISTS bookings_trip_approved_idx;
ALTER TABLE bookings
    DROP CONSTRAINT IF EXISTS bookings_dropoff_stop_fkey,
    DROP CONSTRAINT IF EXISTS bookings_pickup_stop_fkey,
    DROP CONSTRAINT IF EXISTS bookings_message_length,
    DROP CONSTRAINT IF EXISTS bookings_luggage_known,
    DROP CONSTRAINT IF EXISTS bookings_seats_max,
    DROP CONSTRAINT IF EXISTS bookings_segment_order,
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS decided_at,
    DROP COLUMN IF EXISTS message,
    DROP COLUMN IF EXISTS luggage,
    DROP COLUMN IF EXISTS dropoff_order,
    DROP COLUMN IF EXISTS pickup_order;

ALTER TABLE trip_stops
    DROP CONSTRAINT IF EXISTS trip_stops_trip_location_key,
    DROP CONSTRAINT IF EXISTS trip_stops_meeting_point_length,
    DROP CONSTRAINT IF EXISTS trip_stops_type_known,
    DROP COLUMN IF EXISTS stop_type,
    DROP COLUMN IF EXISTS planned_at;
ALTER TABLE trip_stops RENAME COLUMN meeting_point TO note;

DROP INDEX IF EXISTS trips_live_depart_idx;
ALTER TABLE trips
    DROP CONSTRAINT IF EXISTS trips_driver_no_overlap,
    DROP CONSTRAINT IF EXISTS trips_conditions_length,
    DROP CONSTRAINT IF EXISTS trips_note_length,
    DROP CONSTRAINT IF EXISTS trips_conversation_known,
    DROP CONSTRAINT IF EXISTS trips_child_seat_known,
    DROP CONSTRAINT IF EXISTS trips_luggage_size_known,
    DROP CONSTRAINT IF EXISTS trips_pets_policy_known,
    DROP CONSTRAINT IF EXISTS trips_smoking_policy_known,
    DROP CONSTRAINT IF EXISTS trips_music_policy_known,
    DROP CONSTRAINT IF EXISTS trips_audio_system_known,
    DROP CONSTRAINT IF EXISTS trips_air_conditioning_known,
    DROP CONSTRAINT IF EXISTS trips_seats_total_max,
    DROP CONSTRAINT IF EXISTS trips_duration_max,
    DROP CONSTRAINT IF EXISTS trips_arrival_after_departure,
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS conditions,
    DROP COLUMN IF EXISTS conversation,
    DROP COLUMN IF EXISTS child_seat,
    DROP COLUMN IF EXISTS luggage_size,
    DROP COLUMN IF EXISTS pets_policy,
    DROP COLUMN IF EXISTS smoking_policy,
    DROP COLUMN IF EXISTS music_policy,
    DROP COLUMN IF EXISTS audio_system,
    DROP COLUMN IF EXISTS air_conditioning,
    DROP COLUMN IF EXISTS price_negotiable,
    DROP COLUMN IF EXISTS arrives_at;

-- Trips that only existed in the new lifecycle states cannot be represented by the old enum
-- semantics; they are mapped back to the closest old status.
UPDATE trips SET status = 'completed' WHERE status IN ('in_progress', 'expired');
UPDATE bookings SET status = 'approved' WHERE status = 'completed';
UPDATE bookings SET status = 'rejected' WHERE status = 'expired';

ALTER TABLE trips
    ADD COLUMN seats_left integer NOT NULL DEFAULT 0,
    ADD COLUMN pickup_note text,
    ADD COLUMN dropoff_note text;
UPDATE trips SET seats_left = seats_total;
ALTER TABLE trips ADD CONSTRAINT trips_seats_bounds CHECK (seats_left >= 0 AND seats_left <= seats_total);

DROP INDEX IF EXISTS vehicles_user_active_idx;
DROP TRIGGER IF EXISTS vehicles_set_updated_at ON vehicles;
ALTER TABLE vehicles
    DROP CONSTRAINT IF EXISTS vehicles_text_lengths,
    DROP CONSTRAINT IF EXISTS vehicles_passenger_seats_range,
    DROP CONSTRAINT IF EXISTS vehicles_type_known,
    DROP CONSTRAINT IF EXISTS vehicles_year_range,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS photo_url,
    DROP COLUMN IF EXISTS passenger_seats,
    DROP COLUMN IF EXISTS vehicle_type,
    DROP COLUMN IF EXISTS year;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_bio_length,
    DROP CONSTRAINT IF EXISTS users_last_name_length,
    DROP CONSTRAINT IF EXISTS users_first_name_length,
    DROP COLUMN IF EXISTS bio,
    DROP COLUMN IF EXISTS last_name,
    DROP COLUMN IF EXISTS first_name;
