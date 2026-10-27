package main

import (
	"context"
	"errors"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to the isolated PostgreSQL 18 test database")
	}
	adminConfig, err := pgx.ParseConfig(url)
	if err != nil || adminConfig.Database != "endurain_exporter_test" {
		t.Fatal("integration tests require database endurain_exporter_test")
	}
	ctx := context.Background()
	admin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(ctx) })
	if _, err := admin.Exec(ctx, "TRUNCATE public.activities,public.users RESTART IDENTITY CASCADE; INSERT INTO public.users(id,username,email) VALUES (1,'synthetic-alice','alice@example.invalid'),(2,'synthetic-bob','bob@example.invalid'),(3,'synthetic-empty','empty@example.invalid');"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, r := range []struct {
		user, sport, distance, duration int
		days                            int
		elevation, calories             any
		hidden                          bool
	}{
		{1, 1, 12000, 600, 1, 100, 100, false},
		{1, 1, 1000, 60, 40, 10, nil, false},
		{1, 2, 5000, 300, 10, 50, 50, false},
		{2, 1, 8000, 400, 1, nil, nil, false},
		{1, 1, 999999, 9999, 0, 9999, 9999, true},
	} {
		if _, err := admin.Exec(ctx, "INSERT INTO public.activities(user_id,activity_type,distance,total_timer_time,elevation_gain,calories,start_time,is_hidden,visibility) VALUES($1,$2,$3,$4,$5,$6,$7,$8,2)", r.user, r.sport, r.distance, r.duration, r.elevation, r.calories, now.Add(-time.Duration(r.days)*24*time.Hour), r.hidden); err != nil {
			t.Fatal(err)
		}
	}
	roleConfig := adminConfig.Copy()
	roleConfig.User = "endurain_exporter"
	roleConfig.Password = "development-only-exporter-password"
	c := testConfig()
	roleURL, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	roleURL.User = neturl.UserPassword(roleConfig.User, roleConfig.Password)
	c.databaseURL = roleURL.String()
	pool, err := newPool(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var poolUser string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&poolUser); err != nil || poolUser != "endurain_exporter" {
		t.Fatal("integration pool must use the least-privileged role")
	}
	load := func(ctx context.Context) (snapshot, error) { return readSnapshot(ctx, pool, c.maxSeries, false) }
	handler := newHandler(c, load)

	t.Run("multiple_users_sports_and_windows", func(t *testing.T) {
		s, err := load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		families := gather(t, s, 20000)
		for _, tc := range []struct {
			user, sport, period  string
			distance, activities float64
		}{
			{"1", "1", "all", 13000, 2}, {"1", "1", "7d", 12000, 1}, {"1", "1", "30d", 12000, 1},
			{"1", "2", "all", 5000, 1}, {"1", "2", "7d", 0, 0}, {"1", "2", "30d", 5000, 1},
			{"2", "1", "all", 8000, 1},
		} {
			labels := map[string]string{"user_id": tc.user, "sport_id": tc.sport, "period": tc.period}
			requireMetric(t, families, "endurain_activity_distance_meters", labels, tc.distance)
			requireMetric(t, families, "endurain_activities", labels, tc.activities)
		}
		requireMetric(t, families, "endurain_user_info", map[string]string{"user_id": "3"}, 1)
		requireMetric(t, families, "endurain_activity_energy_joules", map[string]string{"user_id": "1", "sport_id": "1", "period": "7d"}, 418400)
		for _, labels := range []map[string]string{{"user_id": "1", "sport_id": "1", "period": "all"}, {"user_id": "2", "sport_id": "1", "period": "all"}} {
			if _, ok := metricValue(families, "endurain_activity_energy_joules", labels); ok {
				t.Fatal("incomplete energy was exposed as a complete total")
			}
		}
		totals := map[int64]float64{}
		for _, row := range s.stats {
			if row.period == "all" {
				totals[row.user] += row.distance
			}
		}
		if totals[1] != 18000 || totals[2] != 8000 || totals[3] != 0 {
			t.Fatalf("incorrect per-user totals: %v", totals)
		}
	})

	t.Run("individual_details_opt_in_units_and_permissions", func(t *testing.T) {
		if _, err := admin.Exec(ctx, `UPDATE activities SET name='Synthetic ride "quoted"',total_elapsed_time=900,end_time=start_time+interval '900 seconds',average_speed=5.5,max_speed=9,average_hr=120,max_hr=150,average_power=200,max_power=400,normalized_power=220,average_cad=80,max_cad=100,pace=0.2,elevation_loss=20 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		details, err := readSnapshot(ctx, pool, 20000, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readSnapshot(ctx, pool, 18, true); !errors.Is(err, errSeriesBudget) {
			t.Fatal("individual detail SQL result budget not enforced")
		}
		if len(details.details) != 4 {
			t.Fatalf("hidden activity included or activity lost: %d", len(details.details))
		}
		families := gather(t, details, 20000)
		labels := map[string]string{"user_id": "1", "sport_id": "1", "activity_id": "1"}
		for name, want := range map[string]float64{"distance_meters": 12000, "duration_seconds": 600, "elapsed_seconds": 900, "average_speed_meters_per_second": 5.5, "max_speed_meters_per_second": 9, "energy_joules": 418400, "average_heart_rate_beats_per_minute": 120, "max_heart_rate_beats_per_minute": 150, "average_power_watts": 200, "max_power_watts": 400, "normalized_power_watts": 220, "average_cadence_per_minute": 80, "max_cadence_per_minute": 100, "pace_seconds_per_meter": 0.2, "elevation_loss_meters": 20} {
			requireMetric(t, families, "endurain_activity_detail_"+name, labels, want)
		}
		for _, row := range details.details {
			if row.id == 1 && (row.values[3].Float64 != float64(now.Add(-24*time.Hour).UnixMicro())/1e6 || row.values[4].Float64-row.values[3].Float64 != 900) {
				t.Fatal("activity timestamps changed")
			}
			if row.id == 5 {
				t.Fatal("hidden activity escaped filter")
			}
		}
		requireMetric(t, families, "endurain_activity_detail_info", map[string]string{"user_id": "1", "sport_id": "1", "activity_id": "1", "activity_name": `Synthetic ride "quoted"`}, 1)
		c.activityDetails = true
		detailHandler := newHandler(c, func(ctx context.Context) (snapshot, error) { return readSnapshot(ctx, pool, c.maxSeries, true) })
		response := scrape(detailHandler, "/metrics", "")
		if !strings.Contains(response.Body.String(), "endurain_activity_detail_info{") || strings.Contains(response.Body.String(), "synthetic-alice") {
			t.Fatal("detail HTTP exposition failed or exposed a user name")
		}
		base, err := load(ctx)
		if err != nil || len(base.details) != 0 {
			t.Fatal("details are not opt-in")
		}
		// A missing optional grant must fail closed, while aggregate collection still works.
		if _, err := admin.Exec(ctx, "REVOKE SELECT (average_speed) ON activities FROM endurain_exporter"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := admin.Exec(ctx, "GRANT SELECT (average_speed) ON activities TO endurain_exporter"); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := readSnapshot(ctx, pool, 20000, true); err == nil {
			t.Fatal("missing detail permissions ignored")
		}
		if _, err := load(ctx); err != nil {
			t.Fatal("optional permissions broke aggregate mode")
		}
	})

	t.Run("read_only_and_column_permissions", func(t *testing.T) {
		role, err := pgx.ConnectConfig(ctx, roleConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer role.Close(ctx)
		var readOnly string
		if err := role.QueryRow(ctx, "SHOW transaction_read_only").Scan(&readOnly); err != nil || readOnly != "on" {
			t.Fatal("role is not read-only by default")
		}
		if _, err := role.Exec(ctx, "SET default_transaction_read_only=off"); err != nil {
			t.Fatal(err)
		}
		for _, sql := range []string{
			"SELECT username FROM public.users", "SELECT private_notes FROM public.activities", "SELECT * FROM public.activities",
			"INSERT INTO public.users(id) VALUES(999)", "UPDATE public.activities SET distance=0", "DELETE FROM public.activities",
			"CREATE TABLE public.forbidden_exporter_table(id integer)", "SET ROLE endurain_owner",
		} {
			_, err := role.Exec(ctx, sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("query was not denied by permissions: %s (error %v)", sql, err)
			}
		}
	})

	t.Run("exact_window_boundaries", func(t *testing.T) {
		now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL TIME ZONE 'Europe/Paris'"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM public.activities"); err != nil {
			t.Fatal(err)
		}
		for _, offset := range []time.Duration{-7 * 24 * time.Hour, -7*24*time.Hour - time.Microsecond, -30 * 24 * time.Hour, time.Microsecond} {
			if _, err := tx.Exec(ctx, "INSERT INTO public.activities(user_id,activity_type,distance,total_timer_time,start_time) VALUES(1,9999,1,1,$1)", now.Add(offset)); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := tx.Query(ctx, activityQuery, now, 100)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]float64{}
		for rows.Next() {
			var r activityStats
			if err := rows.Scan(&r.user, &r.sport, &r.period, &r.activities, &r.distance, &r.duration, &r.elevation, &r.energy, &r.last); err != nil {
				t.Fatal(err)
			}
			counts[r.period] = r.activities
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if counts["all"] != 4 || counts["7d"] != 1 || counts["30d"] != 3 {
			t.Fatalf("wrong inclusive boundaries or future activity included: %v", counts)
		}
	})

	t.Run("new_users_sports_and_deletions", func(t *testing.T) {
		if _, err := admin.Exec(ctx, "INSERT INTO public.users(id) VALUES(4); INSERT INTO public.activities(user_id,activity_type,distance,total_timer_time,start_time) VALUES(4,987654,99,1,now());"); err != nil {
			t.Fatal(err)
		}
		w := scrape(handler, "/metrics", "")
		if !strings.Contains(w.Body.String(), `endurain_activity_distance_meters{period="all",sport_id="987654",user_id="4"} 99`) {
			t.Fatal("new user or unknown sport not discovered")
		}
		if _, err := admin.Exec(ctx, "DELETE FROM public.users WHERE id=4; DELETE FROM public.activities WHERE user_id=1 AND activity_type=2;"); err != nil {
			t.Fatal(err)
		}
		w = scrape(handler, "/metrics", "")
		if strings.Contains(w.Body.String(), `user_id="4"`) || strings.Contains(w.Body.String(), `sport_id="2"`) {
			t.Fatal("deleted series retained")
		}
	})

	t.Run("lock_timeout_and_recovery", func(t *testing.T) {
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "LOCK TABLE public.activities IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		_, err = load(deadline)
		cancel()
		_ = tx.Rollback(ctx)
		if err == nil {
			t.Fatal("locked collection did not time out")
		}
		if _, err := load(ctx); err != nil {
			t.Fatalf("failed to recover: %v", err)
		}
	})

	t.Run("schema_failure_and_recovery", func(t *testing.T) {
		if _, err := admin.Exec(ctx, "ALTER TABLE public.activities RENAME COLUMN calories TO incompatible_calories"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := admin.Exec(ctx, "ALTER TABLE public.activities RENAME COLUMN incompatible_calories TO calories"); err != nil {
				t.Fatal(err)
			}
		}()
		w := scrape(handler, "/metrics", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "endurain_up 0") || strings.Contains(w.Body.String(), "endurain_user_info") {
			t.Fatal("schema error exposed stale or partial data")
		}
		if w := scrape(handler, "/readyz", ""); w.Code != 503 {
			t.Fatal("schema error not detected by readiness")
		}
	})

	t.Run("recovered_and_budget_enforced", func(t *testing.T) {
		if w := scrape(handler, "/metrics", ""); !strings.Contains(w.Body.String(), "endurain_up 1") {
			t.Fatal("schema recovery failed")
		}
		if _, err := readSnapshot(ctx, pool, 1, false); !errors.Is(err, errSeriesBudget) {
			t.Fatal("SQL result budget not enforced")
		}
	})
}
