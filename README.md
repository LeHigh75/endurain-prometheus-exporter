# endurain-prometheus-exporter

A Go exporter that reads [Endurain](https://github.com/endurain-project/endurain)'s PostgreSQL database using a dedicated read-only account. It exposes activity statistics **per user and sport** at `/metrics` for Prometheus and Grafana.

Compatible schemas verified on **Endurain v0.19.4 and v0.20.1 with PostgreSQL 18**. Hidden activities are excluded; private activities are included. User names, emails and GPS tracks are not exported. Optional activity details include activity titles.

## Port

The container listens on **9808**. In `127.0.0.1:9808:9808`, change the first port to choose the host port, for example `127.0.0.1:19080:9808`. Replace `127.0.0.1` with your server's LAN address if Prometheus runs on another machine.

If Prometheus shares the exporter's Docker network, use `endurain-exporter:9808` directly; publishing a host port is unnecessary.

## PostgreSQL permissions

Connect to the Endurain database as an administrator and run the following. Replace `endurain` in the database grant with your actual database name.

```sql
CREATE ROLE endurain_exporter LOGIN
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT
  NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 3;

ALTER ROLE endurain_exporter SET default_transaction_read_only = on;
ALTER ROLE endurain_exporter SET statement_timeout = '5s';

GRANT CONNECT ON DATABASE endurain TO endurain_exporter;
GRANT USAGE ON SCHEMA public TO endurain_exporter;
GRANT SELECT (id) ON public.users TO endurain_exporter;
GRANT SELECT (
  user_id, activity_type, distance, total_timer_time,
  elevation_gain, calories, start_time, is_hidden
) ON public.activities TO endurain_exporter;
```

Set the account's password interactively in `psql`:

```text
\password endurain_exporter
```

For a repeatable permissions setup, use [sql/grants.sql](sql/grants.sql).

## Run with Docker

Create a `.env` file used by either example:

```dotenv
DATABASE_URL=postgresql://endurain_exporter:YOUR_PASSWORD@endurain-db:5432/endurain?sslmode=disable
```

Replace the password, database hostname and database name. URL-encode special characters in the password. `sslmode=disable` is intended for a private Docker network; configure verified TLS when required by your database connection.

Both examples assume PostgreSQL is reachable on an existing Docker network named `endurain-network`. Replace that name with your own.

### Docker Compose

Save as `docker-compose.yml`:

```yaml
services:
  endurain-exporter:
    image: lehigh/endurain-prometheus-exporter:latest
    restart: unless-stopped
    environment:
      DATABASE_URL: ${DATABASE_URL:?DATABASE_URL is required}
      ACTIVITY_DETAILS: ${ACTIVITY_DETAILS:-false}
      MAX_SERIES: ${MAX_SERIES:-20000}
    ports:
      - "127.0.0.1:9808:9808"
    networks:
      - endurain
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]

networks:
  endurain:
    external: true
    name: endurain-network
```

```bash
docker compose up -d
```

### Docker Run

```bash
docker run -d --name endurain-exporter \
  --restart unless-stopped \
  --network endurain-network \
  --env-file .env \
  -p 127.0.0.1:9808:9808 \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  lehigh/endurain-prometheus-exporter:latest
```

Use either example, not both at once. Metrics are available at `http://127.0.0.1:9808/metrics`.

### Optional TLS, Basic Auth or mTLS

[Prometheus exporter-toolkit](https://github.com/prometheus/exporter-toolkit) handles web security when `WEB_CONFIG_FILE` (or `--web.config.file`) points to a mounted YAML file. `--web.listen-address` overrides `LISTEN_ADDRESS`.

Copy [web-config.example.yml](web-config.example.yml) to `web/web.config.yml`, add `server.crt` and `server.key` to that directory, and replace the bcrypt placeholder using `htpasswd -nBC 10 prometheus`. Ensure the container's UID **65532** can read these files. The sample enables HTTPS and Basic Auth; the [toolkit configuration](https://github.com/prometheus/exporter-toolkit/blob/v0.20.0/docs/web-configuration.md) also supports mutual TLS.

Security is optional: leave `WEB_CONFIG_FILE` unset to manage access through your network or reverse proxy. The old `METRICS_TOKEN` and `METRICS_TOKEN_FILE` settings are rejected at startup; remove them when upgrading.

For Docker Compose, add the following to the exporter service:

```yaml
environment:
  WEB_CONFIG_FILE: /etc/endurain-exporter/web.config.yml
volumes:
  - ./web:/etc/endurain-exporter:ro
```

For Docker Run, add these options before the image name:

```bash
-e WEB_CONFIG_FILE=/etc/endurain-exporter/web.config.yml \
-v "$PWD/web:/etc/endurain-exporter:ro"
```

Use `https://` in the Prometheus scrape configuration, with the matching CA and Basic Auth or client certificate. Credentials and certificates reload through the toolkit; changing between HTTP and HTTPS requires a restart. Mount the directory so atomic file replacements are visible.

With a web configuration, security applies to **all HTTP routes**, including `/healthz`. The Docker healthcheck uses TCP for process liveness only; use an authenticated `/readyz` or `endurain_up` to check database collection. If container arguments override the listen address or web configuration, pass the same arguments to the healthcheck command, or use the environment variables.

## Metrics

All metrics are **gauges**. Do not use `rate()` or `increase()` on these values.

| Metric | Meaning |
| --- | --- |
| `endurain_user_info` | User exists, including users without activities; value `1` |
| `endurain_activities` | Number of stored activities |
| `endurain_activity_distance_meters` | Total distance in meters |
| `endurain_activity_duration_seconds` | Total active duration in seconds |
| `endurain_activity_elevation_gain_meters` | Total elevation gain in meters |
| `endurain_activity_energy_joules` | Total energy in joules; 1 kcal = 4,184 J |
| `endurain_last_activity_timestamp_seconds` | Latest activity start time as a Unix timestamp in seconds |
| `endurain_up` | `1` if collection succeeds, `0` otherwise |
| `endurain_scrape_duration_seconds` | Database collection duration in seconds |

Activity totals use `user_id`, `sport_id` and `period` labels. `period` is `all`, `7d` or `30d`; rolling windows use the activity date. The latest activity timestamp uses `user_id` and `sport_id`; `endurain_user_info` uses only `user_id`. The two collection health metrics have no labels.

Elevation and energy are omitted for a sport/period if any activity lacks that measurement or the period is empty. On collection failure, business metrics are omitted rather than replaced with zero. Prometheus history starts when scraping starts; old activities contribute to totals without backfilling their original dates.

### Optional activity details

Set `ACTIVITY_DETAILS=true` in `.env` to expose every non-hidden activity, including private activities, with its ID and title. Apply [sql/grants-details.sql](sql/grants-details.sql) once after the base permissions:

```sql
GRANT SELECT (
  id, name, total_elapsed_time, end_time, average_speed, max_speed,
  elevation_loss, average_hr, max_hr, average_power, max_power,
  normalized_power, average_cad, max_cad, pace
) ON public.activities TO endurain_exporter;
```

These additional gauges use `user_id`, `sport_id` and `activity_id`, without a `period` label. Only `endurain_activity_detail_info` also includes `activity_name`. Missing measurements are omitted; recorded zero values are preserved. Speeds and pace are read directly from Endurain, not recalculated.

| Metric | Meaning |
| --- | --- |
| `endurain_activity_detail_info` | Activity exists, with its title; value `1` |
| `endurain_activity_detail_distance_meters` | Distance |
| `endurain_activity_detail_duration_seconds` | Active timer duration |
| `endurain_activity_detail_elapsed_seconds` | Elapsed duration including pauses |
| `endurain_activity_detail_start_timestamp_seconds` | Start date as Unix seconds |
| `endurain_activity_detail_end_timestamp_seconds` | End date as Unix seconds |
| `endurain_activity_detail_average_speed_meters_per_second` | Recorded average speed |
| `endurain_activity_detail_max_speed_meters_per_second` | Recorded maximum speed |
| `endurain_activity_detail_elevation_gain_meters` | Elevation gain |
| `endurain_activity_detail_elevation_loss_meters` | Elevation loss |
| `endurain_activity_detail_energy_joules` | Energy; 1 kcal = 4,184 J |
| `endurain_activity_detail_average_heart_rate_beats_per_minute` | Average heart rate |
| `endurain_activity_detail_max_heart_rate_beats_per_minute` | Maximum heart rate |
| `endurain_activity_detail_average_power_watts` | Average power |
| `endurain_activity_detail_max_power_watts` | Maximum power |
| `endurain_activity_detail_normalized_power_watts` | Normalized power |
| `endurain_activity_detail_average_cadence_per_minute` | Recorded average cadence; interpretation depends on sport/source |
| `endurain_activity_detail_max_cadence_per_minute` | Recorded maximum cadence; interpretation depends on sport/source |
| `endurain_activity_detail_pace_seconds_per_meter` | Recorded pace |

All activities remain available regardless of Grafana's time range while they are stored in Endurain and scraped. The timestamp metric is the activity date; Prometheus samples retain the scrape time.

`MAX_SERIES` defaults to `20000` and covers both aggregates and details: each activity adds 6 to 19 series. If the total exceeds this limit, the entire collection fails with `endurain_up 0`; nothing is silently truncated. Increase the limit deliberately as your activity history grows, or disable details to retain only aggregates. The access control on `/metrics` covers every user's data.
