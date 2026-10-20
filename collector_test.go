package main

import (
	"context"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gather(t *testing.T, s snapshot, budget int) []*dto.MetricFamily {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector{ctx: context.Background(), load: func(context.Context) (snapshot, error) { return s, nil }, maxSeries: budget})
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	return families
}

func metricValue(families []*dto.MetricFamily, name string, labels map[string]string) (float64, bool) {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != len(labels) {
				continue
			}
			matches := true
			for _, label := range metric.Label {
				if labels[label.GetName()] != label.GetValue() {
					matches = false
				}
			}
			if matches {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func requireMetric(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want float64) {
	t.Helper()
	got, ok := metricValue(families, name, labels)
	if !ok || got != want {
		t.Fatalf("%s %v: got %v (present=%v), want %v", name, labels, got, ok, want)
	}
}

func TestPerUserAndSportMetrics(t *testing.T) {
	s := snapshot{users: []int64{1, 2, 3}, stats: []activityStats{
		{user: 1, sport: 1, period: "all", activities: 2, distance: 12000, duration: 600, last: 123, elevation: pgtype.Float8{Float64: 100, Valid: true}, energy: pgtype.Float8{Float64: 418400, Valid: true}},
		{user: 1, sport: 2, period: "all", activities: 1, distance: 5000, duration: 300, last: 456},
		{user: 2, sport: 1, period: "all", activities: 1, distance: 8000, duration: 400, last: 789},
	}}
	families := gather(t, s, 100)
	for _, tc := range []struct {
		user, sport string
		distance    float64
	}{{"1", "1", 12000}, {"1", "2", 5000}, {"2", "1", 8000}} {
		requireMetric(t, families, "endurain_activity_distance_meters", map[string]string{"user_id": tc.user, "sport_id": tc.sport, "period": "all"}, tc.distance)
	}
	requireMetric(t, families, "endurain_user_info", map[string]string{"user_id": "3"}, 1)
	requireMetric(t, families, "endurain_last_activity_timestamp_seconds", map[string]string{"user_id": "1", "sport_id": "2"}, 456)
	requireMetric(t, families, "endurain_up", nil, 1)
	if _, ok := metricValue(families, "endurain_activity_energy_joules", map[string]string{"user_id": "1", "sport_id": "2", "period": "all"}); ok {
		t.Fatal("unknown energy became zero")
	}
	for _, family := range families {
		if family.GetType() != dto.MetricType_GAUGE {
			t.Fatal("snapshot exposed as counter")
		}
	}
}

func TestBudgetAndInvalidValuesOmitAllBusinessMetrics(t *testing.T) {
	for _, s := range []snapshot{
		{users: []int64{1, 2, 3}},
		{stats: []activityStats{{user: 1, sport: 1, period: "all", distance: math.Inf(1)}}},
		{stats: []activityStats{{user: 1, sport: 1, period: "all", distance: math.NaN()}}},
	} {
		families := gather(t, s, 2)
		requireMetric(t, families, "endurain_up", nil, 0)
		if len(families) != 2 {
			t.Fatal("partial business metrics escaped failed collection")
		}
	}
}
