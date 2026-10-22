package main

import (
	"github.com/jackc/pgx/v5/pgtype"
	"math"
	"testing"
)

func TestActivityDetails(t *testing.T) {
	if len(detailDescs) != len((activityDetail{}).values) {
		t.Fatal("detail SQL values and descriptors do not match")
	}
	row := activityDetail{user: 1, sport: 4, id: 12, name: "Synthetic ride"}
	for i := range row.values {
		row.values[i] = pgtype.Float8{Float64: float64(i), Valid: true}
	}
	other := activityDetail{user: 2, sport: 4, id: 13, name: ""}
	other.values[0] = pgtype.Float8{Float64: 500, Valid: true}
	families := gather(t, snapshot{details: []activityDetail{row, other}}, 100)
	labels := map[string]string{"user_id": "1", "sport_id": "4", "activity_id": "12"}
	names := []string{"distance_meters", "duration_seconds", "elapsed_seconds", "start_timestamp_seconds", "end_timestamp_seconds", "average_speed_meters_per_second", "max_speed_meters_per_second", "elevation_gain_meters", "elevation_loss_meters", "energy_joules", "average_heart_rate_beats_per_minute", "max_heart_rate_beats_per_minute", "average_power_watts", "max_power_watts", "normalized_power_watts", "average_cadence_per_minute", "max_cadence_per_minute", "pace_seconds_per_meter"}
	for i, name := range names {
		requireMetric(t, families, "endurain_activity_detail_"+name, labels, float64(i))
	}
	requireMetric(t, families, "endurain_activity_detail_info", map[string]string{"user_id": "1", "sport_id": "4", "activity_id": "12", "activity_name": "Synthetic ride"}, 1)
	requireMetric(t, families, "endurain_activity_detail_distance_meters", map[string]string{"user_id": "2", "sport_id": "4", "activity_id": "13"}, 500)
	if _, ok := metricValue(families, "endurain_activity_detail_average_speed_meters_per_second", map[string]string{"user_id": "2", "sport_id": "4", "activity_id": "13"}); ok {
		t.Fatal("missing measurement became zero")
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1)} {
		row.values[5] = pgtype.Float8{Float64: bad, Valid: true}
		failed := gather(t, snapshot{users: []int64{1}, details: []activityDetail{row}}, 100)
		requireMetric(t, failed, "endurain_up", nil, 0)
		if len(failed) != 2 {
			t.Fatal("partial details escaped a failed collection")
		}
	}
	row.values[5] = pgtype.Float8{}
	failed := gather(t, snapshot{users: []int64{1}, details: []activityDetail{row}}, 2)
	requireMetric(t, failed, "endurain_up", nil, 0)
	if len(failed) != 2 {
		t.Fatal("details bypassed shared series budget")
	}
}

func TestActivityDetailsSeriesBudgetAtScale(t *testing.T) {
	s := snapshot{}
	for user := int64(1); user <= 50; user++ {
		s.users = append(s.users, user)
	}
	for i := int64(0); i < 1000; i++ {
		row := activityDetail{user: i/20 + 1, sport: i%2 + 1, id: i + 1, name: "Synthetic activity"}
		for j := range row.values {
			row.values[j] = pgtype.Float8{Float64: float64(j), Valid: true}
		}
		s.details = append(s.details, row)
	}
	// Exactly 50 user series and 19 series per activity, without cross-user mixing.
	families := gather(t, s, 19050)
	requireMetric(t, families, "endurain_up", nil, 1)
	requireMetric(t, families, "endurain_activity_detail_info", map[string]string{"user_id": "50", "sport_id": "2", "activity_id": "1000", "activity_name": "Synthetic activity"}, 1)
	failed := gather(t, s, 19049)
	requireMetric(t, failed, "endurain_up", nil, 0)
	if len(failed) != 2 {
		t.Fatal("series budget silently truncated the activity list")
	}
}
