package main

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

//go:embed query.sql
var activityQuery string

var errSeriesBudget = errors.New("series budget exceeded")

type activityStats struct {
	user, sport                          int64
	period                               string
	activities, distance, duration, last float64
	elevation, energy                    pgtype.Float8
}

type snapshot struct {
	users   []int64
	stats   []activityStats
	details []activityDetail
}

func newPool(ctx context.Context, c config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.databaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	pc.MaxConns = 2
	pc.ConnConfig.ConnectTimeout = 3 * time.Second
	pc.ConnConfig.RuntimeParams["application_name"] = "endurain-prometheus-exporter"
	pc.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pc.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(c.timeout.Milliseconds(), 10)
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
	return pgxpool.NewWithConfig(ctx, pc)
}

func readSnapshot(ctx context.Context, pool *pgxpool.Pool, maxSeries int, activityDetails bool) (snapshot, error) {
	var result snapshot
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	rows, err := tx.Query(ctx, "SELECT id FROM public.users ORDER BY id LIMIT $1", maxSeries+1)
	if err != nil {
		return result, err
	}
	result.users, err = pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return result, err
	}
	if len(result.users) > maxSeries {
		return result, errSeriesBudget
	}
	rows, err = tx.Query(ctx, activityQuery, time.Now().UTC(), maxSeries+1)
	if err != nil {
		return result, err
	}
	result.stats, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (activityStats, error) {
		var s activityStats
		err := row.Scan(&s.user, &s.sport, &s.period, &s.activities, &s.distance, &s.duration, &s.elevation, &s.energy, &s.last)
		return s, err
	})
	if err != nil {
		return result, err
	}
	if len(result.stats) > maxSeries {
		return result, errSeriesBudget
	}
	if activityDetails {
		rows, err = tx.Query(ctx, detailQuery, maxSeries/6+1)
		if err != nil {
			return result, err
		}
		result.details, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (activityDetail, error) {
			var detail activityDetail
			dest := []any{&detail.user, &detail.sport, &detail.id, &detail.name}
			for i := range detail.values {
				dest = append(dest, &detail.values[i])
			}
			err := row.Scan(dest...)
			return detail, err
		})
		if err != nil {
			return result, err
		}
		if len(result.details) > maxSeries/6 {
			return result, errSeriesBudget
		}
	}
	return result, tx.Commit(ctx)
}

var (
	userDesc       = prometheus.NewDesc("endurain_user_info", "Endurain user present in the database; IDs only, including users without activities.", []string{"user_id"}, nil)
	activityLabels = []string{"user_id", "sport_id", "period"}
	activityDescs  = []*prometheus.Desc{
		prometheus.NewDesc("endurain_activities", "Activities currently stored, excluding hidden activities.", activityLabels, nil),
		prometheus.NewDesc("endurain_activity_distance_meters", "Stored activity distance in meters.", activityLabels, nil),
		prometheus.NewDesc("endurain_activity_duration_seconds", "Stored active timer duration in seconds.", activityLabels, nil),
		prometheus.NewDesc("endurain_activity_elevation_gain_meters", "Elevation gain in meters; omitted if any activity in the period lacks this value.", activityLabels, nil),
		prometheus.NewDesc("endurain_activity_energy_joules", "Energy in joules (4184 joules per kcal); omitted if any activity in the period lacks this value.", activityLabels, nil),
	}
	lastDesc     = prometheus.NewDesc("endurain_last_activity_timestamp_seconds", "Start timestamp of the latest stored non-hidden activity.", []string{"user_id", "sport_id"}, nil)
	upDesc       = prometheus.NewDesc("endurain_up", "Whether the complete database collection succeeded.", nil, nil)
	durationDesc = prometheus.NewDesc("endurain_scrape_duration_seconds", "Time spent collecting the database snapshot.", nil, nil)
)

func businessMetrics(s snapshot, maxSeries int) ([]prometheus.Metric, error) {
	metrics := make([]prometheus.Metric, 0, len(s.users))
	add := func(desc *prometheus.Desc, value float64, labels ...string) error {
		if len(metrics) >= maxSeries {
			return errSeriesBudget
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("non-finite database value")
		}
		metrics = append(metrics, prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...))
		return nil
	}
	for _, user := range s.users {
		if err := add(userDesc, 1, strconv.FormatInt(user, 10)); err != nil {
			return nil, err
		}
	}
	for _, row := range s.stats {
		user, sport := strconv.FormatInt(row.user, 10), strconv.FormatInt(row.sport, 10)
		values := []pgtype.Float8{{Float64: row.activities, Valid: true}, {Float64: row.distance, Valid: true}, {Float64: row.duration, Valid: true}, row.elevation, row.energy}
		for i, value := range values {
			if value.Valid {
				if err := add(activityDescs[i], value.Float64, user, sport, row.period); err != nil {
					return nil, err
				}
			}
		}
		if row.period == "all" {
			if err := add(lastDesc, row.last, user, sport); err != nil {
				return nil, err
			}
		}
	}
	for _, row := range s.details {
		labels := []string{strconv.FormatInt(row.user, 10), strconv.FormatInt(row.sport, 10), strconv.FormatInt(row.id, 10)}
		if err := add(detailInfoDesc, 1, append(labels, row.name)...); err != nil {
			return nil, err
		}
		for i, value := range row.values {
			if value.Valid {
				if err := add(detailDescs[i], value.Float64, labels...); err != nil {
					return nil, err
				}
			}
		}
	}
	return metrics, nil
}

type collector struct {
	ctx       context.Context
	load      func(context.Context) (snapshot, error)
	maxSeries int
}

func (c collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- userDesc
	for _, desc := range activityDescs {
		ch <- desc
	}
	ch <- detailInfoDesc
	for _, desc := range detailDescs {
		ch <- desc
	}
	ch <- lastDesc
	ch <- upDesc
	ch <- durationDesc
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	s, err := c.load(c.ctx)
	var metrics []prometheus.Metric
	if err == nil {
		metrics, err = businessMetrics(s, c.maxSeries)
	}
	up := 0.0
	if err == nil {
		up = 1
		for _, metric := range metrics {
			ch <- metric
		}
	} else {
		category := "database"
		var pgErr *pgconn.PgError
		switch {
		case errors.Is(err, errSeriesBudget):
			category = "series_budget"
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			category = "timeout_or_cancellation"
		case errors.As(err, &pgErr):
			category = "postgres_" + pgErr.Code
		}
		// Driver errors may contain credentials or SQL; log only their category.
		slog.Warn("collection failed", "category", category)
	}
	ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, up)
	ch <- prometheus.MustNewConstMetric(durationDesc, prometheus.GaugeValue, time.Since(start).Seconds())
}
