# Operations

## Database access

Use the dedicated read-only role and grants described in [README.md](../README.md). Optional activity details require [sql/grants-details.sql](../sql/grants-details.sql). The exporter never applies migrations or changes application data.

Queries share a read-only repeatable-read transaction, a two-connection pool and the configured collection timeout. Both aggregate and individual metrics count towards `MAX_SERIES`; an overflow omits all business metrics and reports `endurain_up 0`.

## Configuration and security

See [.env.example](../.env.example) for every setting. The database URL is the only required setting. `DATABASE_URL_FILE` is an alternative to `DATABASE_URL`; an explicit file must exist and contain a nonempty URL. Do not configure both sources.

HTTP security is provided exclusively by Prometheus exporter-toolkit. Set `WEB_CONFIG_FILE` or `--web.config.file` to a mounted YAML file for TLS, Basic Auth or mTLS. Without a web configuration, access control belongs to the surrounding network or reverse proxy. The removed `METRICS_TOKEN` and `METRICS_TOKEN_FILE` settings fail startup instead of silently disabling authentication.

Mount the configuration directory read-only and allow UID 65532 to read its files. Passwords in the web configuration use bcrypt hashes; plaintext scrape credentials belong in a secret manager. Credentials and certificates reload through the toolkit, while switching between HTTP and HTTPS requires a restart.

All routes share toolkit protection. A credential grants access to every user's exported data; the user selector in Grafana is not an access-control boundary. Hidden activities are excluded, private activities are included, and optional activity titles are exposed only on the activity-info metric. GPS tracks, private notes, names and emails of users are never read for export.

## Health and collection

- `/healthz` checks HTTP process liveness without querying PostgreSQL.
- `/readyz` runs the complete SQL collection and budget checks; failures return HTTP 503.
- `/metrics` reports `endurain_up 0` on collection failures and omits business metrics. Missing optional measurements are omitted rather than converted to zero.
- With `WEB_CONFIG_FILE`, the native container probe uses TCP for process liveness because the HTTP routes may require Basic Auth or a client certificate. Use authenticated readiness and Prometheus collection health to assess the database.
- If CLI arguments override the listen address or web configuration, pass those same arguments to the healthcheck command, or use the environment variables.

Metrics are current snapshots, not counters: do not apply `rate()` or `increase()`. Activity dates are metric values; sample timestamps remain scrape times. Historical activities are available in detail mode while they remain in Endurain, without backfilling old Prometheus samples.

## Prometheus scrape

Example for toolkit Basic Auth over HTTPS:

```yaml
scrape_configs:
  - job_name: endurain
    scrape_interval: 60s
    scrape_timeout: 10s
    scheme: https
    tls_config:
      ca_file: /run/secrets/endurain_ca
    basic_auth:
      username: prometheus
      password_file: /run/secrets/endurain_metrics_password
    static_configs:
      - targets: [endurain-exporter:9808]
```

The password file contains only the plaintext password matching the bcrypt hash. For mTLS, use the matching client certificate and key instead of Basic Auth. Inspect remote-write destinations before collecting personal activity data.

## Validation and upgrades

The isolated development stack uses public demo Basic Auth credentials from `tests/web.config.yml`:

```bash
docker compose -f docker-compose.dev.yml up -d --build --wait
python3 tests/smoke.py
```

To test without built-in security:

```bash
WEB_CONFIG_FILE= docker compose -f docker-compose.dev.yml up -d --wait exporter
WEB_CONFIG_FILE= python3 tests/smoke.py
```

When upgrading from the removed Bearer mode, prepare the toolkit configuration and matching Prometheus credentials first. Remove the old token variables from the exporter, mount the web configuration, and update the scrape to Basic Auth or mTLS in the same deployment. Keep the previous deployment available until the new authenticated scrape is verified. Do not remove live credentials before that transition.

## Rollback and backups

Pin deployed images by digest. Rollback restores both the old image and its matching security configuration and scrape credentials. The exporter has no persistent application state and never modifies Endurain records.

Database and secret backups remain part of the Endurain and secret-manager backup procedures. The development PostgreSQL database is synthetic and ephemeral; stopping the stack removes its data.
