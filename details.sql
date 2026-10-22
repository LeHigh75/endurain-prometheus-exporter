SELECT user_id, activity_type, id, coalesce(name, ''),
    distance::double precision,
    total_timer_time::double precision,
    total_elapsed_time::double precision,
    extract(epoch FROM start_time)::double precision,
    extract(epoch FROM end_time)::double precision,
    average_speed::double precision,
    max_speed::double precision,
    elevation_gain::double precision,
    elevation_loss::double precision,
    calories::double precision * 4184,
    average_hr::double precision,
    max_hr::double precision,
    average_power::double precision,
    max_power::double precision,
    normalized_power::double precision,
    average_cad::double precision,
    max_cad::double precision,
    pace::double precision
FROM public.activities
WHERE NOT is_hidden
ORDER BY user_id, id
LIMIT $1;
