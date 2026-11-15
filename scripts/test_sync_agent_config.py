import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("sync_config", Path(__file__).with_name("sync-agent-config.py"))
sync_config = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync_config)


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.calls = []
        self.skills = {}
        self.fail = False
        self.redirect = False
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def respond(self, data, status=200):
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"data": data, "error": {"code": "", "statusCode": 0}}).encode())

            def do_GET(self):
                owner.calls.append(("GET", self.path, None, self.headers.get("Authorization")))
                if owner.redirect:
                    self.send_response(307)
                    self.send_header("Location", owner.base + "/redirected")
                    self.end_headers()
                    return
                self.respond({"skills": list(owner.skills.values())})

            def mutate(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                owner.calls.append((self.command, self.path, body, self.headers.get("Authorization")))
                if owner.fail:
                    self.respond({"secret": "do-not-print-me"}, 502)
                    return
                if self.path.endswith("/skills"):
                    owner.skills[body["name"]] = {**body, "scope": "user"}
                if "/skills/" in self.path:
                    name = self.path.rsplit("/", 1)[-1]
                    owner.skills[name].update(body)
                self.respond({"name": body.get("name", "updated")})

            do_POST = mutate
            do_PUT = mutate

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.addCleanup(self.server.server_close)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.shutdown)
        self.base = "http://127.0.0.1:" + str(self.server.server_port) + "/api/v1"
        self.skills_file = self.write("skills.json", [{"name": "review", "description": "Review", "instruction": "Inspect tests"}])
        self.mcp_file = self.write("mcp.json", {"mcpServers": {"demo": {"command": "runner", "env": {"KEY": "do-not-print-me"}}}})

    def write(self, name, content):
        path = Path(self.temp.name) / name
        path.write_text(json.dumps(content), encoding="utf-8")
        return str(path)

    def run_cli(self, *extra, token=True):
        out, err = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, {"ECHO_SERVICE_ACCESS_TOKEN": "test-only" if token else ""}), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            result = sync_config.main(["--service-url", self.base, "--container-id", "one", "--skills", self.skills_file, "--mcp", self.mcp_file, *extra])
        self.assertNotIn("do-not-print-me", out.getvalue() + err.getvalue())
        self.assertNotIn("test-only", out.getvalue() + err.getvalue())
        return result, out.getvalue(), err.getvalue()

    def test_default_dry_run_has_no_network_effect(self):
        result, out, _ = self.run_cli(token=False)
        self.assertEqual(result, 0)
        self.assertIn("dry-run", out)
        self.assertEqual(self.calls, [])

    def test_apply_upserts_without_duplicate_or_prune_and_uses_bearer(self):
        self.skills["unrelated"] = {"name": "unrelated", "scope": "user"}
        self.assertEqual(self.run_cli("--apply")[0], 0)
        self.assertEqual(self.run_cli("--apply")[0], 0)
        mutations = [call for call in self.calls if call[0] != "GET"]
        self.assertEqual([call[0] for call in mutations], ["POST", "POST", "PUT", "POST"])
        self.assertTrue(all(call[3] == "Bearer test-only" for call in self.calls))
        self.assertEqual(set(self.skills), {"review", "unrelated"})
        self.assertEqual(mutations[1][1], "/api/v1/containers/one/mcp-servers")
        self.assertEqual(json.loads(mutations[1][2]["config"])["mcpServers"]["demo"]["env"]["KEY"], "do-not-print-me")

    def test_validates_all_inputs_before_mutating(self):
        self.write("mcp.json", {"mcpServers": {"bad": {"url": "file:///etc/passwd"}}})
        self.assertEqual(self.run_cli("--apply")[0], 1)
        self.assertEqual(self.calls, [])

    def test_missing_token_and_unsafe_url_fail_without_requests(self):
        self.assertEqual(self.run_cli("--apply", token=False)[0], 1)
        with self.assertRaises(ValueError):
            sync_config.validate_service_url("http://public.example/api/v1")
        with self.assertRaises(ValueError):
            sync_config.validate_service_url("https://user:password@example.com/api/v1")
        self.assertEqual(self.calls, [])

    def test_partial_failure_stops_and_does_not_print_response_or_retry(self):
        self.fail = True
        result, _, err = self.run_cli("--apply")
        self.assertEqual(result, 1)
        self.assertIn("reconcile", err)
        self.assertEqual(len([call for call in self.calls if call[0] != "GET"]), 1)

    def test_redirect_is_not_followed_with_authorization(self):
        self.redirect = True
        self.assertEqual(self.run_cli("--apply")[0], 1)
        self.assertEqual(len(self.calls), 1)

    def test_rejects_duplicate_names_and_malformed_skill(self):
        for content in [
            [{"name": "../bad", "description": "d", "instruction": "i"}],
            [{"name": "ok", "description": "d", "instruction": ""}],
            [{"name": "ok", "description": "d", "instruction": "i"}] * 2,
            [{"name": "ok", "description": "d", "instruction": "i", "scope": "enterprise"}],
        ]:
            self.write("skills.json", content)
            self.assertEqual(self.run_cli("--apply")[0], 1)
        self.assertEqual(self.calls, [])


if __name__ == "__main__":
    unittest.main()
