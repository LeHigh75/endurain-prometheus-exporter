# endurain-prometheus-exporter

A Go exporter that reads [Endurain](https://github.com/endurain-project/endurain)'s PostgreSQL database using a dedicated read-only account. It exposes activity statistics **per user and sport** at `/metrics` for Prometheus and Grafana.

Verified with **Endurain v0.19.4 and PostgreSQL 18**. Hidden activities are excluded; private activities are included. Names, emails and GPS tracks are not exported.

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
      METRICS_TOKEN: ${METRICS_TOKEN:-}
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

**Optional authentication:** add `METRICS_TOKEN` to `.env` with at least 32 random printable ASCII characters without whitespace to require `Authorization: Bearer <token>`. Leave it unset to manage access through your network or reverse proxy.

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
