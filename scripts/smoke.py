#!/usr/bin/env python3
"""Start a real binary, verify wire behavior, then check SIGTERM shutdown."""
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

root = Path(__file__).resolve().parents[1]
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    port = listener.getsockname()[1]
base = f"http://127.0.0.1:{port}"
with tempfile.TemporaryDirectory() as state, tempfile.TemporaryFile() as log:
    env = dict(os.environ, ECHO_HTTPSERVER_LISTEN=f"127.0.0.1:{port}", ECHO_DB_PATH=str(Path(state) / "echo.db"))
    process = subprocess.Popen([str(root / "output/bin/echoservice-server")], cwd=root, env=env, stdout=log, stderr=log)
    try:
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError("server exited before becoming ready")
            try:
                with urllib.request.urlopen(base + "/readyz", timeout=0.2) as response:
                    assert response.status == 200
                break
            except (urllib.error.URLError, TimeoutError):
                time.sleep(0.05)
        else:
            raise RuntimeError("server did not become ready")
        for path in ["/echo-service/api/v1/GetServiceInfo", "/echo-service/openapi/v1/GetServiceInfo"]:
            req = urllib.request.Request(base + path, data=b"{}", headers={"Content-Type": "application/json", "Request-ID": "smoke-test", "Connection": "close"})
            with urllib.request.urlopen(req, timeout=2) as response:
                body = json.load(response)
                assert body["data"]["name"] == "echoservice"
                assert body["error"]["requestId"] == "smoke-test"
        req = urllib.request.Request(base + "/echo-service/gateway/v1/chat/completions", data=b"{}", headers={"Connection": "close"})
        try:
            urllib.request.urlopen(req, timeout=2)
            raise AssertionError("gateway unexpectedly reported success")
        except urllib.error.HTTPError as response:
            with response:
                assert response.code == 501
                assert json.load(response)["error"]["code"] == "GatewayNotImplemented"
        unauth = urllib.request.Request(base + "/api/v1/me", headers={"Connection": "close"})
        try:
            urllib.request.urlopen(unauth, timeout=2)
            raise AssertionError("protected API accepted an anonymous request")
        except urllib.error.HTTPError as response:
            with response:
                assert response.code == 401
        process.send_signal(signal.SIGTERM)
        assert process.wait(timeout=12) == 0
        print("PASS: SQLite readiness, protected API, console/openapi, gateway boundary, SIGTERM")
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)
