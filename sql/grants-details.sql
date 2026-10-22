\set ON_ERROR_STOP on

-- Optional: run after grants.sql when enabling ACTIVITY_DETAILS.
BEGIN;
GRANT SELECT (
    id, name, total_elapsed_time, end_time, average_speed, max_speed,
    elevation_loss, average_hr, max_hr, average_power, max_power,
    normalized_power, average_cad, max_cad, pace
) ON public.activities TO endurain_exporter;
COMMIT;
