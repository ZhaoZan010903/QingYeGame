import argparse
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import acceptance_suite as suite


class AcceptanceHelperTests(unittest.TestCase):
    def test_publication_wait_is_read_only_and_rejects_unfinished_states(self):
        check = suite.Suite.__new__(suite.Suite)
        check.report = {"checks": []}
        client = Mock()
        check.clients = {"admin": client}
        client.request.side_effect = [suite.Reply(200, json.dumps({"data": {"status": status, "version": 9}}).encode())
                                      for status in ("publishing", "published")]
        with patch.object(suite.time, "sleep"), patch.object(suite.time, "monotonic", side_effect=[0, 1, 2]):
            result = check.wait_published("fixture", {"status": "publishing", "version": 8})
        self.assertEqual(result, {"status": "published", "version": 9})
        self.assertEqual([call.args for call in client.request.call_args_list],
                         [("GET", "/api/v1/admin/products/fixture")] * 2)
        client.reset_mock()
        for state, clocks in (("rejected", [0]), ("publishing", [0, 46])):
            with self.subTest(state=state), patch.object(suite.time, "monotonic", side_effect=clocks):
                with self.assertRaises(suite.CheckFailure):
                    check.wait_published("fixture", {"status": state})
        client.request.assert_not_called()

    def test_only_loopback_without_credentials(self):
        for value in ("http://127.0.0.1:5184", "http://[::1]:5184", "http://localhost:5185"):
            self.assertEqual(suite.local_url(value), value)
        for value in ("https://127.0.0.1:5184", "http://8.140.230.149:80", "http://example.com:5184", "http://user:secret@127.0.0.1:5184", "http://127.0.0.1:5184/path", "http://127.0.0.1:5184?token=secret"):
            with self.assertRaises(argparse.ArgumentTypeError):
                suite.local_url(value)

    def test_percentiles(self):
        self.assertEqual(suite.percentile(list(range(1, 101)), 50), 50)
        self.assertEqual(suite.percentile(list(range(1, 101)), 95), 95)
        self.assertEqual(suite.percentile(list(range(1, 101)), 99), 99)
        self.assertIsNone(suite.percentile([], 99))

    def test_offline_favorite_assertion_targets_only_this_fixture(self):
        older_public = {"id": "previous-run", "status": "published", "description": "Isolated acceptance fixture. Still public.", "images": [{"url": "/public-image"}], "image_total": 1}
        offline = {"id": "current-run", "status": "offline", "images": [], "image_total": 0}
        self.assertTrue(suite.offline_favorite_is_private([older_public, offline], "current-run"))
        for change in ({"description": "private introduction"}, {"images": [{"url": "/private-image"}]}, {"image_total": 1}, {"status": "published"}):
            self.assertFalse(suite.offline_favorite_is_private([older_public, dict(offline, **change)], "current-run"))
        self.assertFalse(suite.offline_favorite_is_private([older_public], "current-run"))
        self.assertFalse(suite.offline_favorite_is_private([offline, offline], "current-run"))

    def test_png_is_bounded_and_pixel_bomb_has_only_small_header(self):
        import struct
        normal, bomb = suite.png_bytes(), suite.oversized_pixels_png()
        self.assertEqual(normal[:8], b"\x89PNG\r\n\x1a\n")
        self.assertEqual(struct.unpack(">II", normal[16:24]), (2, 2))
        self.assertEqual(struct.unpack(">II", bomb[16:24]), (16001, 16001))
        self.assertLess(len(bomb), 200)

    def test_redirect_not_followed_and_cookie_is_internal(self):
        class Handler(BaseHTTPRequestHandler):
            hits = 0

            def do_GET(self):
                Handler.hits += 1
                self.send_response(302)
                self.send_header("Location", "http://example.invalid/do-not-follow")
                self.send_header("Set-Cookie", "qingye_session=private-test-token; HttpOnly; Path=/")
                self.end_headers()

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            origin = f"http://127.0.0.1:{server.server_port}"
            client = suite.Client(origin, origin)
            reply = client.request("GET", "/api/v1/test")
            self.assertEqual(reply.status, 302)
            self.assertEqual(Handler.hits, 1)
            self.assertIn("private-test-token", client.cookie_header())
            with self.assertRaises(suite.CheckFailure):
                client.request("PUT", "http://example.invalid/upload?token=secret", b"bytes")
        finally:
            server.shutdown()
            server.server_close()
            worker.join(2)

    def test_failed_report_contains_no_credentials_or_cookies(self):
        with tempfile.TemporaryDirectory() as temp:
            temp = Path(temp)
            accounts = {"accounts": [{"role": role, "phone": f"private-phone-{i}", "password": "DO-NOT-WRITE-PASSWORD"} for i, role in enumerate(("admin", "member", "member"))]}
            account_file = temp / "accounts.json"
            account_file.write_text(json.dumps(accounts), encoding="utf-8")
            args = argparse.Namespace(public_url="http://127.0.0.1:5184", admin_url="http://127.0.0.1:5185", public_origin="http://127.0.0.1:5186", admin_origin="http://127.0.0.1:5187", account_file=str(account_file), state=str(temp / "state.json"), output=str(temp / "report.json"), phase="functional", max_upload_bytes=10485760)
            check = suite.Suite(args)

            class FakeClient:
                def __init__(self, role):
                    self.role = role
                    self.cookies = {"private-cookie": "DO-NOT-WRITE-COOKIE"}

                def request(self, *args, **kwargs):
                    if args[1] == "/healthz":
                        return suite.Reply(200, json.dumps({"surface": "admin" if self.role == "admin" else "public"}).encode())
                    return suite.Reply(200, json.dumps({"data": {"id": "fixture-" + self.role, "role": self.role, "token": "DO-NOT-WRITE-TOKEN"}}).encode())

            for actor in ("admin", "member", "other"):
                check.clients[actor] = FakeClient("admin" if actor == "admin" else "member")
            check.clients["anonymous"] = FakeClient("member")
            check.functional = lambda: check.check("intentional assertion", False)
            self.assertFalse(check.run())
            result = (temp / "report.json").read_text(encoding="utf-8") + (temp / "state.json").read_text(encoding="utf-8")
            for value in ("DO-NOT-WRITE-PASSWORD", "DO-NOT-WRITE-COOKIE", "DO-NOT-WRITE-TOKEN", "private-phone"):
                self.assertNotIn(value, result)


if __name__ == "__main__":
    unittest.main()
