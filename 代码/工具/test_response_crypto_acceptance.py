import base64
import gzip
import json
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

import acceptance_suite
from response_crypto_acceptance import PROTOCOL, WIRE_TYPE, decrypt_http, encrypted_client, request_headers


def encoded(value):
    return base64.urlsafe_b64encode(value).decode().rstrip("=")


CONTEXT = {"enabled": True, "protocol": PROTOCOL, "surface": "public", "context_id": "fixture-context",
           "key_id": "fixture-key", "key": encoded(bytes(range(32))), "server_time": 100,
           "rotate_at": 3700, "retain_until": 3820}


def envelope(method="GET", target="/api/v1/products?page=2", status=200, request_id="request-1234567890",
             encoding="identity", plaintext=b'{"data":{"id":"fixture"}}'):
    mime = "application/json; charset=utf-8"
    aad = "\n".join(["QY-R24-HTTP", "1", "public", "fixture-context", "fixture-key", request_id,
                      method, target, str(status), mime, encoding]).encode()
    nonce = bytes(range(12))
    payload = gzip.compress(plaintext) if encoding == "gzip" else plaintext
    ciphertext = AESGCM(bytes(range(32))).encrypt(nonce, payload, aad)
    headers = {**request_headers(CONTEXT, request_id), "Content-Type": WIRE_TYPE,
               "X-QY-Nonce": encoded(nonce), "X-QY-Content-Type": mime, "X-QY-Content-Encoding": encoding}
    return ciphertext, headers


class ResponseCryptoClientTests(unittest.TestCase):
    def test_revoked_identity_requires_both_context_rejection_and_business_401(self):
        suite = acceptance_suite.Suite.__new__(acceptance_suite.Suite)
        suite.args = SimpleNamespace(response_crypto=True)
        suite.report = {"checks": []}
        replies = [acceptance_suite.Reply(409, b'{"code":"RESPONSE_CONTEXT_CHANGED","message":"changed"}',
                                          {"X-QY-Protocol-Error": "1"}), acceptance_suite.Reply(401)]
        client = SimpleNamespace(request=lambda *args: replies.pop(0))
        suite.revoked_identity(client, "revoked")
        self.assertEqual(len(replies), 0)
        self.assertEqual(len(suite.report["checks"]), 2)

    def test_unrelated_409_cannot_satisfy_revoked_identity_check(self):
        suite = acceptance_suite.Suite.__new__(acceptance_suite.Suite)
        suite.args = SimpleNamespace(response_crypto=True)
        suite.report = {"checks": []}
        client = SimpleNamespace(request=lambda *args: acceptance_suite.Reply(409, b'{"code":"VERSION_CONFLICT"}'))
        with self.assertRaises(acceptance_suite.CheckFailure):
            suite.revoked_identity(client, "revoked")

    def test_authenticated_gzip_preserves_json(self):
        payload = json.dumps({"data": ["商品图片"] * 600}, ensure_ascii=False).encode()
        body, headers = envelope(encoding="gzip", plaintext=payload)
        plain, _ = decrypt_http(body, headers, 200, "GET", "/api/v1/products?page=2", CONTEXT, "request-1234567890")
        self.assertEqual(plain, payload)
        self.assertLess(len(body), len(payload))

    def test_gzip_decoded_body_is_bounded(self):
        body, headers = envelope(encoding="gzip", plaintext=b"x" * (4 * 1024 * 1024 + 1))
        with self.assertRaisesRegex(ValueError, "exceeds limit"):
            decrypt_http(body, headers, 200, "GET", "/api/v1/products?page=2", CONTEXT, "request-1234567890")

    def test_binary_response_and_business_error_are_authenticated(self):
        for status in (200, 403, 409, 422):
            with self.subTest(status=status):
                body, headers = envelope(status=status)
                plain, mime = decrypt_http(body, headers, status, "GET", "/api/v1/products?page=2", CONTEXT, "request-1234567890")
                self.assertEqual(json.loads(plain)["data"]["id"], "fixture")
                self.assertTrue(mime.startswith("application/json"))

    def test_tampering_or_different_request_is_rejected(self):
        body, headers = envelope()
        for method, target, status in (("POST", "/api/v1/products?page=2", 200),
                                       ("GET", "/api/v1/products?page=3", 200),
                                       ("GET", "/api/v1/products?page=2", 201)):
            with self.subTest(method=method, target=target, status=status), self.assertRaises(Exception):
                decrypt_http(body, headers, status, method, target, CONTEXT, "request-1234567890")
        for name, value in (("X-QY-Context", "other"), ("X-QY-Key-Id", "other"),
                            ("X-QY-Request-Id", "other"), ("X-QY-Nonce", "AA"),
                            ("X-QY-Content-Type", "text/plain"), ("X-QY-Content-Encoding", "gzip")):
            with self.subTest(header=name), self.assertRaises(Exception):
                decrypt_http(body, dict(headers, **{name: value}), 200, "GET", "/api/v1/products?page=2", CONTEXT, "request-1234567890")
        with self.assertRaises(Exception):
            decrypt_http(body[:-1] + bytes([body[-1] ^ 1]), headers, 200, "GET", "/api/v1/products?page=2", CONTEXT, "request-1234567890")

    def test_decryption_failure_does_not_replay_business_write(self):
        Client = encrypted_client(acceptance_suite.Client, acceptance_suite.Reply, acceptance_suite.CheckFailure)
        client = Client("http://127.0.0.1:5184", "http://127.0.0.1:5186")
        calls = []

        def raw_request(instance, method, path, body=None, **kwargs):
            calls.append((method, path))
            if path.endswith("/init"):
                return acceptance_suite.Reply(200, json.dumps({"data": CONTEXT}).encode())
            return acceptance_suite.Reply(200, b'{"data":"unexpected plaintext"}', {"Content-Type": "application/json"})

        with patch.object(acceptance_suite.Client, "request", raw_request), self.assertRaises(acceptance_suite.CheckFailure):
            client.request("POST", "/api/v1/me/products", {"request_id": "unchanged-id"})
        self.assertEqual(calls, [("GET", "/api/v1/response-crypto/init"), ("POST", "/api/v1/me/products")])

    def test_disabled_server_cannot_silently_downgrade(self):
        Client = encrypted_client(acceptance_suite.Client, acceptance_suite.Reply, acceptance_suite.CheckFailure)
        client = Client("http://127.0.0.1:5184", "http://127.0.0.1:5186")
        with patch.object(acceptance_suite.Client, "request", return_value=acceptance_suite.Reply(200, b'{"data":{"enabled":false}}')) as send:
            with self.assertRaises(acceptance_suite.CheckFailure):
                client.request("POST", "/api/v1/me/products", {})
        self.assertEqual(send.call_count, 1)


if __name__ == "__main__":
    unittest.main()
