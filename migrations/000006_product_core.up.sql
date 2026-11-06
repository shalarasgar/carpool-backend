-- Product schema: profiles, vehicles, routes with stops, segment bookings, reviews, notifications.
--
-- Seat availability has one source of truth: approved bookings. Free seats on a route
-- segment are computed inside the transaction that holds the trip row lock, so the old
-- trips.seats_left counter is dropped instead of being kept in sync.

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Users: mandatory first and last name are enforced by the service before any trip or
-- booking. Rows created by OTP login start empty until the profile is completed.
ALTER TABLE users
    ADD COLUMN first_name text NOT NULL DEFAULT '',
    ADD COLUMN last_name text NOT NULL DEFAULT '',
    ADD COLUMN bio text,
    ADD CONSTRAINT users_first_name_length CHECK (char_length(first_name) <= 50),
    ADD CONSTRAINT users_last_name_length CHECK (char_length(last_name) <= 50),
    ADD CONSTRAINT users_bio_length CHECK (bio IS NULL OR char_length(bio) <= 500);

-- Vehicles: a user may own several. Deleting a vehicle is a soft delete so past trips
-- keep their vehicle reference.
ALTER TABLE vehicles
    ADD COLUMN year smallint,
    ADD COLUMN vehicle_type text,
    ADD COLUMN passenger_seats smallint,
    ADD COLUMN photo_url text,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN deleted_at timestamptz,
    ADD CONSTRAINT vehicles_year_range CHECK (year IS NULL OR year BETWEEN 1950 AND 2100),
    ADD CONSTRAINT vehicles_type_known CHECK (
        vehicle_type IS NULL
        OR vehicle_type IN ('sedan', 'hatchback', 'wagon', 'suv', 'minivan', 'minibus', 'other')
    ),
    ADD CONSTRAINT vehicles_passenger_seats_range CHECK (passenger_seats IS NULL OR passenger_seats BETWEEN 1 AND 8),
    ADD CONSTRAINT vehicles_text_lengths CHECK (
        char_length(brand) <= 40 AND char_length(model) <= 40
        AND char_length(color) <= 30 AND char_length(plate) <= 20
    );

CREATE TRIGGER vehicles_set_updated_at
BEFORE UPDATE ON vehicles
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE INDEX vehicles_user_active_idx ON vehicles (user_id) WHERE deleted_at IS NULL;

-- Trips.
ALTER TABLE trips DROP CONSTRAINT trips_seats_bounds;
ALTER TABLE trips
    DROP COLUMN seats_left,
    DROP COLUMN pickup_note,
    DROP COLUMN dropoff_note;

ALTER TABLE trips
    ADD COLUMN arrives_at timestamptz,
    ADD COLUMN price_negotiable boolean NOT NULL DEFAULT false,
    ADD COLUMN air_conditioning text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN audio_system text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN music_policy text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN smoking_policy text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN pets_policy text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN luggage_size text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN child_seat text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN conversation text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN conditions text,
    ADD COLUMN cancelled_at timestamptz,
    ADD COLUMN completed_at timestamptz;

UPDATE trips SET arrives_at = departs_at + interval '4 hours' WHERE arrives_at IS NULL;

ALTER TABLE trips
    ALTER COLUMN arrives_at SET NOT NULL,
    ADD CONSTRAINT trips_arrival_after_departure CHECK (arrives_at > departs_at),
    ADD CONSTRAINT trips_duration_max CHECK (arrives_at <= departs_at + interval '48 hours'),
    ADD CONSTRAINT trips_seats_total_max CHECK (seats_total <= 8),
    ADD CONSTRAINT trips_air_conditioning_known CHECK (air_conditioning IN ('yes', 'no', 'unspecified')),
    ADD CONSTRAINT trips_audio_system_known CHECK (audio_system IN ('yes', 'no', 'unspecified')),
    ADD CONSTRAINT trips_music_policy_known CHECK (music_policy IN ('allowed', 'none', 'driver_decides', 'unspecified')),
    ADD CONSTRAINT trips_smoking_policy_known CHECK (smoking_policy IN ('allowed', 'prohibited', 'unspecified')),
    ADD CONSTRAINT trips_pets_policy_known CHECK (pets_policy IN ('allowed', 'not_allowed', 'unspecified')),
    ADD CONSTRAINT trips_luggage_size_known CHECK (luggage_size IN ('none', 'small', 'medium', 'large', 'unspecified')),
    ADD CONSTRAINT trips_child_seat_known CHECK (child_seat IN ('yes', 'no', 'unspecified')),
    ADD CONSTRAINT trips_conversation_known CHECK (conversation IN ('friendly', 'quiet', 'no_preference', 'unspecified')),
    ADD CONSTRAINT trips_note_length CHECK (note IS NULL OR char_length(note) <= 1000),
    ADD CONSTRAINT trips_conditions_length CHECK (conditions IS NULL OR char_length(conditions) <= 500);

-- Last line of defence for the driver schedule rule. The service also checks a
-- configurable buffer under a lock on the driver's users row; this constraint makes two
-- overlapping live trips of one driver impossible even if that check were bypassed.
ALTER TABLE trips
    ADD CONSTRAINT trips_driver_no_overlap EXCLUDE USING gist (
        driver_id WITH =,
        tstzrange(departs_at, arrives_at, '[)') WITH &&
    ) WHERE (status IN ('active', 'full', 'in_progress'));

CREATE INDEX trips_live_depart_idx ON trips (departs_at) WHERE status IN ('active', 'full');

-- Route stops. stop_order 0 is the departure, the highest order is the destination.
ALTER TABLE trip_stops RENAME COLUMN note TO meeting_point;
ALTER TABLE trip_stops
    ADD COLUMN planned_at timestamptz,
    ADD COLUMN stop_type text NOT NULL DEFAULT 'both',
    ADD CONSTRAINT trip_stops_type_known CHECK (stop_type IN ('pickup', 'dropoff', 'both')),
    ADD CONSTRAINT trip_stops_meeting_point_length CHECK (meeting_point IS NULL OR char_length(meeting_point) <= 200),
    ADD CONSTRAINT trip_stops_trip_location_key UNIQUE (trip_id, location_id);

-- Trips created before this migration get a two-stop route from their endpoints.
INSERT INTO trip_stops (trip_id, location_id, stop_order, planned_at, stop_type)
SELECT t.id, t.from_location_id, 0, t.departs_at, 'pickup'
FROM trips t
WHERE NOT EXISTS (SELECT 1 FROM trip_stops s WHERE s.trip_id = t.id);
INSERT INTO trip_stops (trip_id, location_id, stop_order, planned_at, stop_type)
SELECT t.id, t.to_location_id, 1, t.arrives_at, 'dropoff'
FROM trips t
WHERE (SELECT COUNT(*) FROM trip_stops s WHERE s.trip_id = t.id) = 1;

-- Bookings cover the segment [pickup_order, dropoff_order) of the route.
ALTER TABLE bookings
    ADD COLUMN pickup_order integer,
    ADD COLUMN dropoff_order integer,
    ADD COLUMN luggage text NOT NULL DEFAULT 'unspecified',
    ADD COLUMN message text,
    ADD COLUMN decided_at timestamptz,
    ADD COLUMN cancelled_at timestamptz,
    ADD COLUMN completed_at timestamptz;

UPDATE bookings b SET
    pickup_order = 0,
    dropoff_order = (SELECT MAX(stop_order) FROM trip_stops s WHERE s.trip_id = b.trip_id)
WHERE pickup_order IS NULL;

ALTER TABLE bookings
    ALTER COLUMN pickup_order SET NOT NULL,
    ALTER COLUMN dropoff_order SET NOT NULL,
    ADD CONSTRAINT bookings_segment_order CHECK (pickup_order >= 0 AND pickup_order < dropoff_order),
    ADD CONSTRAINT bookings_seats_max CHECK (seats <= 8),
    ADD CONSTRAINT bookings_luggage_known CHECK (luggage IN ('none', 'small', 'medium', 'large', 'unspecified')),
    ADD CONSTRAINT bookings_message_length CHECK (message IS NULL OR char_length(message) <= 500),
    ADD CONSTRAINT bookings_pickup_stop_fkey FOREIGN KEY (trip_id, pickup_order)
        REFERENCES trip_stops (trip_id, stop_order),
    ADD CONSTRAINT bookings_dropoff_stop_fkey FOREIGN KEY (trip_id, dropoff_order)
        REFERENCES trip_stops (trip_id, stop_order);

CREATE INDEX bookings_trip_approved_idx ON bookings (trip_id) WHERE status = 'approved';
CREATE INDEX bookings_passenger_created_idx ON bookings (passenger_id, created_at DESC);

-- Reviews belong to one completed booking. A review row without a booking cannot exist.
ALTER TABLE reviews
    ADD COLUMN booking_id uuid REFERENCES bookings (id);
ALTER TABLE reviews
    ALTER COLUMN booking_id SET NOT NULL,
    ADD CONSTRAINT reviews_booking_author_key UNIQUE (booking_id, from_user_id),
    ADD CONSTRAINT reviews_comment_length CHECK (comment IS NULL OR char_length(comment) <= 1000);

CREATE INDEX reviews_to_user_created_idx ON reviews (to_user_id, created_at DESC);

-- In-app notification inbox. The client renders localized text from type and references,
-- so a row never carries phone numbers or OTP codes. dedupe_key makes event delivery
-- idempotent: the same event inserted twice is stored once.
CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    type text NOT NULL,
    trip_id uuid REFERENCES trips (id),
    booking_id uuid REFERENCES bookings (id),
    actor_id uuid REFERENCES users (id),
    dedupe_key text NOT NULL,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notifications_type_known CHECK (type IN (
        'booking_requested', 'booking_approved', 'booking_rejected',
        'booking_cancelled_by_passenger', 'booking_cancelled_by_driver', 'booking_expired',
        'trip_cancelled', 'trip_updated', 'trip_reminder', 'trip_completed', 'review_received'
    )),
    CONSTRAINT notifications_user_dedupe_key UNIQUE (user_id, dedupe_key)
);

CREATE INDEX notifications_user_created_idx ON notifications (user_id, created_at DESC);
CREATE INDEX notifications_user_unread_idx ON notifications (user_id) WHERE read_at IS NULL;
