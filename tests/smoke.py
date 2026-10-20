"""Smoke-test the isolated development stack, using its public dummy token."""

import os
from urllib.error import HTTPError
from urllib.request import Request, urlopen

base = f"http://127.0.0.1:{os.environ.get('EXPORTER_PORT', '9808')}"
token = "development-only-metrics-token-change-me"


def request(path, authorization=None):
    headers = {"Authorization": authorization} if authorization else {}
    try:
        with urlopen(Request(base + path, headers=headers), timeout=10) as response:
            return response.status, response.headers, response.read().decode()
    except HTTPError as error:
        return error.code, error.headers, error.read().decode()


for path in ["/metrics", "/readyz", "/metrics?token=" + token]:
    for authorization in [None, "Bearer invalid"]:
        status, headers, body = request(path, authorization)
        assert status == 401, (path, status)
        assert headers["Cache-Control"] == "no-store"
        assert "endurain_user_info" not in body

assert request("/healthz")[0] == 200
assert request("/readyz", "Bearer " + token)[0] == 200
status, headers, body = request("/metrics", "Bearer " + token)
assert status == 200 and "endurain_up 1\n" in body
assert headers["Cache-Control"] == "no-store"
assert "synthetic-alice" not in body and "@example.invalid" not in body
print("Smoke OK: authentication, liveness, readiness and PostgreSQL collection")
