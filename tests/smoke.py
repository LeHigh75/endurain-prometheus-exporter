"""Smoke-test the isolated development stack with or without builtin auth."""

import os
from urllib.error import HTTPError
from urllib.request import Request, urlopen

base = f"http://127.0.0.1:{os.environ.get('EXPORTER_PORT', '9808')}"
token = os.environ.get("METRICS_TOKEN", "development-only-metrics-token-change-me")


def request(path, authorization=None):
    headers = {"Authorization": authorization} if authorization else {}
    try:
        with urlopen(Request(base + path, headers=headers), timeout=10) as response:
            return response.status, response.headers, response.read().decode()
    except HTTPError as error:
        return error.code, error.headers, error.read().decode()


if token:
    for path in ["/metrics", "/readyz", "/metrics?token=" + token]:
        for authorization in [None, "Bearer invalid"]:
            status, headers, body = request(path, authorization)
            assert status == 401, (path, status)
            assert headers["Cache-Control"] == "no-store"
            assert "endurain_user_info" not in body
else:
    for path in ["/metrics", "/readyz"]:
        for authorization in [None, "Basic external-proxy-auth"]:
            status, headers, _ = request(path, authorization)
            assert status == 200, (path, status)
            assert headers.get("WWW-Authenticate") is None
            assert headers["Cache-Control"] == "no-store"

assert request("/healthz")[0] == 200
authorization = "Bearer " + token if token else None
assert request("/readyz", authorization)[0] == 200
status, headers, body = request("/metrics", authorization)
assert status == 200 and "endurain_up 1\n" in body
assert headers["Cache-Control"] == "no-store"
assert "synthetic-alice" not in body and "@example.invalid" not in body
print("Smoke OK: configured auth mode, liveness, readiness and PostgreSQL collection")
