-- Core schema for the carpool API.
-- PostgreSQL 16: gen_random_uuid() is built in. timestamptz everywhere.

CREATE TYPE location_type AS ENUM ('welayat', 'city', 'etrap');
CREATE TYPE trip_status AS ENUM ('active', 'full', 'cancelled', 'completed');
CREATE TYPE booking_status AS ENUM (
    'pending',
    'approved',
    'rejected',
    'cancelled_by_passenger',
    'cancelled_by_driver'
);
CREATE TYPE sms_status AS ENUM ('pending', 'processing', 'sent', 'failed');

CREATE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    phone text NOT NULL,
    name text NOT NULL DEFAULT '',
    photo_url text,
    rating_avg numeric(3, 2) NOT NULL DEFAULT 0,
    rating_count integer NOT NULL DEFAULT 0,
    locale text NOT NULL DEFAULT 'tk',
    is_blocked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_phone_e164 CHECK (phone ~ '^\+993[0-9]{8}$'),
    CONSTRAINT users_phone_key UNIQUE (phone),
    CONSTRAINT users_rating_avg_range CHECK (rating_avg >= 0 AND rating_avg <= 5),
    CONSTRAINT users_rating_count_nonnegative CHECK (rating_count >= 0),
    CONSTRAINT users_locale_supported CHECK (locale IN ('tk', 'ru', 'en'))
);

CREATE TRIGGER users_set_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TABLE vehicles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    brand text NOT NULL,
    model text NOT NULL,
    color text NOT NULL,
    plate text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT vehicles_brand_not_empty CHECK (char_length(brand) > 0),
    CONSTRAINT vehicles_model_not_empty CHECK (char_length(model) > 0),
    CONSTRAINT vehicles_color_not_empty CHECK (char_length(color) > 0),
    CONSTRAINT vehicles_plate_not_empty CHECK (char_length(plate) > 0)
);

CREATE INDEX vehicles_user_id_idx ON vehicles (user_id);

CREATE TABLE locations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id uuid REFERENCES locations (id),
    type location_type NOT NULL,
    name_tk text NOT NULL,
    name_ru text NOT NULL,
    name_en text NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT locations_parent_not_self CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT locations_parent_matches_type CHECK (
        (type = 'welayat' AND parent_id IS NULL)
        OR type IN ('city', 'etrap')
    ),
    CONSTRAINT locations_names_not_empty CHECK (
        char_length(name_tk) > 0
        AND char_length(name_ru) > 0
        AND char_length(name_en) > 0
    ),
    CONSTRAINT locations_sort_order_nonnegative CHECK (sort_order >= 0)
);

CREATE INDEX locations_parent_id_idx ON locations (parent_id);
CREATE INDEX locations_type_active_sort_idx ON locations (type, is_active, sort_order);

CREATE TABLE trips (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id uuid NOT NULL REFERENCES users (id),
    vehicle_id uuid REFERENCES vehicles (id) ON DELETE SET NULL,
    from_location_id uuid NOT NULL REFERENCES locations (id),
    to_location_id uuid NOT NULL REFERENCES locations (id),
    departs_at timestamptz NOT NULL,
    seats_total integer NOT NULL,
    seats_left integer NOT NULL,
    suggested_price numeric(10, 2),
    currency text NOT NULL DEFAULT 'TMT',
    pickup_note text,
    dropoff_note text,
    note text,
    status trip_status NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT trips_seats_bounds CHECK (seats_left >= 0 AND seats_left <= seats_total),
    CONSTRAINT trips_seats_total_positive CHECK (seats_total > 0),
    CONSTRAINT trips_distinct_endpoints CHECK (from_location_id <> to_location_id),
    CONSTRAINT trips_price_nonnegative CHECK (suggested_price IS NULL OR suggested_price >= 0),
    CONSTRAINT trips_currency_code CHECK (char_length(currency) = 3)
);

CREATE TRIGGER trips_set_updated_at
BEFORE UPDATE ON trips
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE INDEX trips_route_depart_idx ON trips (from_location_id, to_location_id, departs_at);
CREATE INDEX trips_status_depart_idx ON trips (status, departs_at);
CREATE INDEX trips_driver_id_idx ON trips (driver_id);

CREATE TABLE trip_stops (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id uuid NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    location_id uuid NOT NULL REFERENCES locations (id),
    stop_order integer NOT NULL,
    note text,
    CONSTRAINT trip_stops_order_nonnegative CHECK (stop_order >= 0),
    CONSTRAINT trip_stops_trip_order_key UNIQUE (trip_id, stop_order)
);

CREATE INDEX trip_stops_trip_id_idx ON trip_stops (trip_id);
CREATE INDEX trip_stops_location_id_idx ON trip_stops (location_id);

CREATE TABLE bookings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id uuid NOT NULL REFERENCES trips (id),
    passenger_id uuid NOT NULL REFERENCES users (id),
    seats integer NOT NULL,
    status booking_status NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT bookings_seats_positive CHECK (seats > 0)
);

CREATE TRIGGER bookings_set_updated_at
BEFORE UPDATE ON bookings
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

-- One active (pending or approved) booking per passenger per trip.
CREATE UNIQUE INDEX bookings_one_active_per_passenger_idx
    ON bookings (trip_id, passenger_id)
    WHERE status IN ('pending', 'approved');

CREATE INDEX bookings_trip_id_idx ON bookings (trip_id);
CREATE INDEX bookings_passenger_id_idx ON bookings (passenger_id);

CREATE TABLE reviews (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id uuid NOT NULL REFERENCES trips (id),
    from_user_id uuid NOT NULL REFERENCES users (id),
    to_user_id uuid NOT NULL REFERENCES users (id),
    rating smallint NOT NULL,
    comment text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reviews_rating_range CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT reviews_distinct_users CHECK (from_user_id <> to_user_id),
    CONSTRAINT reviews_trip_pair_key UNIQUE (trip_id, from_user_id, to_user_id)
);

CREATE INDEX reviews_to_user_id_idx ON reviews (to_user_id);
CREATE INDEX reviews_trip_id_idx ON reviews (trip_id);

CREATE TABLE user_blocks (
    blocker_id uuid NOT NULL REFERENCES users (id),
    blocked_id uuid NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CONSTRAINT user_blocks_distinct_users CHECK (blocker_id <> blocked_id)
);

CREATE INDEX user_blocks_blocked_id_idx ON user_blocks (blocked_id);

CREATE TABLE reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id uuid NOT NULL REFERENCES users (id),
    reported_user_id uuid NOT NULL REFERENCES users (id),
    trip_id uuid REFERENCES trips (id),
    reason text NOT NULL,
    details text,
    status text NOT NULL DEFAULT 'open',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reports_reason_not_empty CHECK (char_length(reason) > 0),
    CONSTRAINT reports_distinct_users CHECK (reporter_id <> reported_user_id)
);

CREATE INDEX reports_reported_user_id_idx ON reports (reported_user_id);
CREATE INDEX reports_status_created_idx ON reports (status, created_at);
CREATE INDEX reports_trip_id_idx ON reports (trip_id);

CREATE TABLE otp_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    phone text NOT NULL,
    code_hash text NOT NULL,
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    used boolean NOT NULL DEFAULT false,
    ip text,
    device_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT otp_codes_phone_e164 CHECK (phone ~ '^\+993[0-9]{8}$'),
    CONSTRAINT otp_codes_hash_not_empty CHECK (char_length(code_hash) > 0),
    CONSTRAINT otp_codes_attempts_nonnegative CHECK (attempts >= 0)
);

CREATE INDEX otp_codes_phone_created_idx ON otp_codes (phone, created_at DESC);

CREATE TABLE refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    token_hash text NOT NULL,
    device_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT refresh_tokens_hash_not_empty CHECK (char_length(token_hash) > 0),
    CONSTRAINT refresh_tokens_hash_key UNIQUE (token_hash)
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_user_device_idx ON refresh_tokens (user_id, device_id);

CREATE TABLE sms_gateways (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    api_key_hash text NOT NULL,
    last_heartbeat_at timestamptz,
    is_active boolean NOT NULL DEFAULT true,
    operator_prefixes text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sms_gateways_name_not_empty CHECK (char_length(name) > 0),
    CONSTRAINT sms_gateways_name_key UNIQUE (name),
    CONSTRAINT sms_gateways_api_key_hash_key UNIQUE (api_key_hash)
);

CREATE INDEX sms_gateways_active_heartbeat_idx
    ON sms_gateways (last_heartbeat_at)
    WHERE is_active;

CREATE TABLE sms_queue (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    phone text NOT NULL,
    message text NOT NULL,
    status sms_status NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    gateway_id uuid REFERENCES sms_gateways (id),
    last_error text,
    locked_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    CONSTRAINT sms_queue_phone_e164 CHECK (phone ~ '^\+993[0-9]{8}$'),
    CONSTRAINT sms_queue_message_not_empty CHECK (char_length(message) > 0),
    CONSTRAINT sms_queue_attempts_nonnegative CHECK (attempts >= 0)
);

CREATE INDEX sms_queue_pending_created_idx
    ON sms_queue (created_at)
    WHERE status = 'pending';

CREATE INDEX sms_queue_processing_locked_idx
    ON sms_queue (locked_until)
    WHERE status = 'processing';

CREATE INDEX sms_queue_gateway_id_idx ON sms_queue (gateway_id);

CREATE TABLE device_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    platform text NOT NULL,
    token text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT device_tokens_platform_not_empty CHECK (char_length(platform) > 0),
    CONSTRAINT device_tokens_token_not_empty CHECK (char_length(token) > 0),
    CONSTRAINT device_tokens_user_token_key UNIQUE (user_id, token)
);

CREATE INDEX device_tokens_user_id_idx ON device_tokens (user_id);
