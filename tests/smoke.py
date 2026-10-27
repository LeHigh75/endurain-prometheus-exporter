"""Smoke-test the isolated stack with toolkit Basic Auth or no builtin auth."""

import base64
import os
from urllib.error import HTTPError
from urllib.request import Request, urlopen

base = f"http://127.0.0.1:{os.environ.get('EXPORTER_PORT', '9808')}"
secured = bool(os.environ.get("WEB_CONFIG_FILE", "/etc/endurain-exporter/web.config.yml"))
# Public demo credentials match tests/web.config.yml; never use in production.
authorization = "Basic " + base64.b64encode(
    b"prometheus:development-only-metrics-password"
).decode() if secured else None


def request(path, header=None):
    headers = {"Authorization": header} if header else {}
    try:
        with urlopen(Request(base + path, headers=headers), timeout=10) as response:
            return response.status, response.headers, response.read().decode()
    except HTTPError as error:
        return error.code, error.headers, error.read().decode()


for path in ["/metrics", "/readyz", "/healthz"]:
    for header in [None, "Bearer obsolete", "Basic invalid"]:
        status, headers, body = request(path, header)
        if secured:
            assert status == 401, (path, status)
            assert "endurain_user_info" not in body
        else:
            assert status == 200, (path, status)
            assert headers.get("WWW-Authenticate") is None
            assert headers["Cache-Control"] == "no-store"
    assert request(path, authorization)[0] == 200

status, headers, body = request("/metrics", authorization)
assert status == 200 and "endurain_up 1\n" in body
assert headers["Cache-Control"] == "no-store"
assert "synthetic-alice" not in body and "@example.invalid" not in body
print("Smoke OK: toolkit security mode, liveness, readiness and PostgreSQL collection")
