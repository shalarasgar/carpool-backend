package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shalarasgar/carpool-backend/internal/activity"
)

// SaveActivity writes hourly and daily user counts. A bucket already stored keeps the
// larger count: the running hour is written again on every flush, and after a restart
// the API starts counting the same hour from zero.
func (db *AppDB) SaveActivity(ctx context.Context, s activity.Snapshot) error {
	if s.Empty() {
		return nil
	}
	return db.inTx(ctx, "save activity", func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, h := range s.Hours {
			batch.Queue(`
				INSERT INTO activity_hours (hour_start, users) VALUES ($1, $2)
				ON CONFLICT (hour_start) DO UPDATE SET users = GREATEST(activity_hours.users, EXCLUDED.users)`,
				h.Start, h.Users)
		}
		for _, d := range s.Days {
			batch.Queue(`
				INSERT INTO activity_days (day, users) VALUES ($1::date, $2)
				ON CONFLICT (day) DO UPDATE SET users = GREATEST(activity_days.users, EXCLUDED.users)`,
				d.Day.Format(time.DateOnly), d.Users)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return dbErr("save activity", err)
		}
		return nil
	})
}

// ActivityCounts returns the hourly counts from hoursFrom, the daily counts from the
// calendar day of daysFrom in loc, and the first hour ever counted (nil if none).
func (db *AppDB) ActivityCounts(ctx context.Context, hoursFrom, daysFrom time.Time, loc *time.Location) (
	[]activity.HourCount, []activity.DayCount, *time.Time, error,
) {
	var hours []activity.HourCount
	rows, err := db.pool.Query(ctx,
		`SELECT hour_start, users FROM activity_hours WHERE hour_start >= $1 ORDER BY hour_start`, hoursFrom)
	if err != nil {
		return nil, nil, nil, dbErr("activity hours", err)
	}
	for rows.Next() {
		var h activity.HourCount
		if err := rows.Scan(&h.Start, &h.Users); err != nil {
			rows.Close()
			return nil, nil, nil, dbErr("scan activity hour", err)
		}
		hours = append(hours, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, dbErr("activity hours", err)
	}

	var days []activity.DayCount
	rows, err = db.pool.Query(ctx,
		`SELECT day::text, users FROM activity_days WHERE day >= $1::date ORDER BY day`,
		daysFrom.In(loc).Format(time.DateOnly))
	if err != nil {
		return nil, nil, nil, dbErr("activity days", err)
	}
	for rows.Next() {
		var raw string
		var d activity.DayCount
		if err := rows.Scan(&raw, &d.Users); err != nil {
			rows.Close()
			return nil, nil, nil, dbErr("scan activity day", err)
		}
		if d.Day, err = time.ParseInLocation(time.DateOnly, raw, loc); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		days = append(days, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, dbErr("activity days", err)
	}

	var first *time.Time
	if err := db.pool.QueryRow(ctx, `SELECT MIN(hour_start) FROM activity_hours`).Scan(&first); err != nil {
		return nil, nil, nil, dbErr("first activity hour", err)
	}
	return hours, days, first, nil
}
