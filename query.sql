WITH periods(name, days) AS (
    VALUES ('all', NULL::integer), ('7d', 7), ('30d', 30)
)
SELECT
    a.user_id,
    a.activity_type,
    p.name,
    count(*) FILTER (WHERE w.included)::double precision,
    coalesce(sum(a.distance) FILTER (WHERE w.included), 0)::double precision,
    coalesce(sum(a.total_timer_time) FILTER (WHERE w.included), 0)::double precision,
    CASE WHEN count(*) FILTER (WHERE w.included) = count(a.elevation_gain) FILTER (WHERE w.included)
         THEN sum(a.elevation_gain) FILTER (WHERE w.included)::double precision END,
    CASE WHEN count(*) FILTER (WHERE w.included) = count(a.calories) FILTER (WHERE w.included)
         THEN sum(a.calories) FILTER (WHERE w.included)::double precision * 4184 END,
    extract(epoch FROM max(a.start_time))::double precision
FROM public.activities AS a
CROSS JOIN periods AS p
CROSS JOIN LATERAL (
    SELECT p.days IS NULL OR (
        -- Seconds keep rolling windows exact across daylight-saving changes.
        a.start_time >= $1::timestamptz - make_interval(secs => p.days * 86400)
        AND a.start_time <= $1::timestamptz
    ) AS included
) AS w
WHERE NOT a.is_hidden
GROUP BY a.user_id, a.activity_type, p.name
ORDER BY a.user_id, a.activity_type, p.name
LIMIT $2;
