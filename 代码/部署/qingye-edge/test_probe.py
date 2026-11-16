"""验证运维 GET 检查真实完成 R24 绑定认证，不以协议错误代替业务权限结果。"""
import base64
import json
import unittest
from unittest import mock

from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.exceptions import InvalidTag

import probe


def encoded(value):
    return base64.urlsafe_b64encode(value).decode().rstrip("=")


CONTEXT = {"enabled": True, "protocol": probe.PROTOCOL, "surface": "public",
           "context_id": "fixture_context", "key_id": "fixture_key", "key": encoded(bytes(range(32)))}
PATH = "/api/v1/auth/capabilities"
REQUEST_ID = "fixture_request_0123456789"


def envelope(context=CONTEXT, status=200, path=PATH, request_id=REQUEST_ID):
    original = "application/json; charset=utf-8"
    aad = "\n".join(["QY-R24-HTTP", "1", context["surface"], context["context_id"], context["key_id"],
                     request_id, "GET", path, str(status), original, "identity"]).encode()
    nonce = bytes(range(12))
    body = AESGCM(bytes(range(32))).encrypt(nonce, b'{"data":{"ok":true}}', aad)
    headers = {"x-qy-protocol": probe.PROTOCOL, "x-qy-context": context["context_id"],
               "x-qy-key-id": context["key_id"], "x-qy-request-id": request_id,
               "content-type": probe.WIRE_TYPE, "x-qy-content-type": original,
               "x-qy-content-encoding": "identity", "x-qy-nonce": encoded(nonce)}
    return body, headers


class R24ProbeTests(unittest.TestCase):
    def test_success_and_business_401_are_decrypted(self):
        for status in (200, 401):
            body, headers = envelope(status=status)
            self.assertTrue(probe.decrypt_response(body, headers, status, PATH, CONTEXT, REQUEST_ID)["data"]["ok"])

    def test_route_and_status_tampering_rejected(self):
        body, headers = envelope()
        for path, status in ((PATH + "?other=1", 200), (PATH, 401)):
            with self.assertRaises(InvalidTag):
                probe.decrypt_response(body, headers, status, path, CONTEXT, REQUEST_ID)

    def test_context_headers_and_ciphertext_tampering_rejected(self):
        body, headers = envelope()
        for name in ("x-qy-key-id", "x-qy-context", "x-qy-request-id"):
            with self.assertRaisesRegex(ValueError, "binding mismatch"):
                probe.decrypt_response(body, dict(headers, **{name: "other"}), 200, PATH, CONTEXT, REQUEST_ID)
        with self.assertRaises(InvalidTag):
            probe.decrypt_response(body[:-1] + bytes([body[-1] ^ 1]), headers, 200, PATH, CONTEXT, REQUEST_ID)

    def test_plain_428_never_counts_as_business_response(self):
        with self.assertRaisesRegex(ValueError, "binding mismatch"):
            probe.decrypt_response(b'{"code":"RESPONSE_PROTOCOL_REQUIRED"}',
                                   {"x-qy-protocol-error": "1"}, 428, PATH, CONTEXT, REQUEST_ID)

    def test_gzip_not_silently_accepted_without_negotiation(self):
        body, headers = envelope()
        with self.assertRaisesRegex(ValueError, "unnegotiated"):
            probe.decrypt_response(body, dict(headers, **{"x-qy-content-encoding": "gzip"}),
                                   200, PATH, CONTEXT, REQUEST_ID)

    def test_init_cookie_context_reused_with_exact_get_headers(self):
        calls = []
        jars = []
        def send(host, port, path, connect_host, **kwargs):
            calls.append(path)
            jars.append(kwargs["cookie_jar"])
            if path == probe.INIT_PATH:
                self.assertEqual(kwargs["extra_headers"]["X-QY-Init"], "1")
                kwargs["cookie_jar"]["qingye_response_public"] = "fixture-cookie"
                return 200, {}, json.dumps({"data": CONTEXT}).encode()
            self.assertEqual(kwargs["cookie_jar"]["qingye_response_public"], "fixture-cookie")
            self.assertNotIn("X-QY-Accept-Encoding", kwargs["extra_headers"])
            self.assertEqual(kwargs["extra_headers"]["X-QY-Context"], CONTEXT["context_id"])
            body, headers = envelope(request_id=kwargs["extra_headers"]["X-QY-Request-Id"])
            return 200, headers, body
        with mock.patch.object(probe, "request", side_effect=send):
            status, _ = probe.encrypted_get("www.qingyegame.com", 443, PATH, "127.0.0.1", "public")
        self.assertEqual(status, 200)
        self.assertEqual(calls, [probe.INIT_PATH, PATH])
        self.assertIs(jars[0], jars[1])

    def test_disabled_crypto_or_wrong_surface_stops_before_business_get(self):
        for change in ({"enabled": False}, {"surface": "admin"}):
            context = dict(CONTEXT, **change)
            with mock.patch.object(probe, "request", return_value=(200, {}, json.dumps({"data": context}).encode())) as send:
                with self.assertRaisesRegex(ValueError, "policy mismatch"):
                    probe.encrypted_get("www.qingyegame.com", 443, PATH, "127.0.0.1", "public")
                self.assertEqual(send.call_count, 1)


if __name__ == "__main__":
    unittest.main()
