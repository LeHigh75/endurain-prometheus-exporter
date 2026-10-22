package main

import (
	_ "embed"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
)

//go:embed details.sql
var detailQuery string

type activityDetail struct {
	user, sport, id int64
	name            string
	values          [18]pgtype.Float8
}

var detailLabels = []string{"user_id", "sport_id", "activity_id"}
var detailInfoDesc = prometheus.NewDesc("endurain_activity_detail_info", "Individual non-hidden activity; title is exposed only when ACTIVITY_DETAILS is enabled.", []string{"user_id", "sport_id", "activity_id", "activity_name"}, nil)

// Order matches the numeric columns in details.sql.
var detailDescs = []*prometheus.Desc{
	prometheus.NewDesc("endurain_activity_detail_distance_meters", "Individual activity distance in meters.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_duration_seconds", "Individual activity active timer duration in seconds.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_elapsed_seconds", "Individual activity elapsed duration including pauses, in seconds.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_start_timestamp_seconds", "Individual activity start as a Unix timestamp in seconds.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_end_timestamp_seconds", "Individual activity end as a Unix timestamp in seconds.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_average_speed_meters_per_second", "Average speed stored by Endurain, in meters per second; not recomputed from distance and duration.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_max_speed_meters_per_second", "Maximum speed stored by Endurain, in meters per second.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_elevation_gain_meters", "Individual activity elevation gain in meters.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_elevation_loss_meters", "Individual activity elevation loss in meters.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_energy_joules", "Individual activity energy in joules (4184 joules per kcal).", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_average_heart_rate_beats_per_minute", "Individual activity average heart rate in beats per minute.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_max_heart_rate_beats_per_minute", "Individual activity maximum heart rate in beats per minute.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_average_power_watts", "Individual activity average power in watts.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_max_power_watts", "Individual activity maximum power in watts.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_normalized_power_watts", "Individual activity normalized power in watts.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_average_cadence_per_minute", "Average cadence stored by Endurain; cycles per minute, with sport/source-specific cadence semantics.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_max_cadence_per_minute", "Maximum cadence stored by Endurain; cycles per minute, with sport/source-specific cadence semantics.", detailLabels, nil),
	prometheus.NewDesc("endurain_activity_detail_pace_seconds_per_meter", "Pace stored by Endurain in seconds per meter.", detailLabels, nil),
}
