-- How many different people used the app in each hour and on each day, for pricing ad
-- slots by time of day. Only the counts are stored: the API keeps the user ids of the
-- current hour and day in memory to count each person once, and never writes them.
-- hour_start is the start of an hour and day a calendar date, both in APP_TIMEZONE.
CREATE TABLE activity_hours (
    hour_start timestamptz PRIMARY KEY,
    users      integer NOT NULL CHECK (users >= 0)
);

CREATE TABLE activity_days (
    day   date PRIMARY KEY,
    users integer NOT NULL CHECK (users >= 0)
);
