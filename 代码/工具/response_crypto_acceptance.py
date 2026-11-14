"""Independent R24 wire client for guarded HTTP acceptance, never a browser bypass.

Requires cryptography for AES-GCM. Secrets remain in memory; callers log only
assertions and fixture IDs. Business requests are never automatically replayed.
"""
import base64
import gzip
import http.cookies
import io
import threading
import time
import urllib.parse
import uuid

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

PROTOCOL = "qy-r24-aesgcm-v1"
INIT_PATH = "/api/v1/response-crypto/init"
WIRE_TYPE = "application/vnd.qingye.encrypted"


def unbase64(value):
    return base64.b64decode(value + "=" * (-len(value) % 4), altchars=b"-_", validate=True)


def request_headers(context, request_id):
    return {"X-QY-Protocol": PROTOCOL, "X-QY-Context": context["context_id"],
            "X-QY-Key-Id": context["key_id"], "X-QY-Request-Id": request_id}


def decrypt_http(raw, headers, status, method, target, context, request_id):
    """Authenticate routing metadata against the request, not against itself."""
    headers = {key.lower(): value for key, value in headers.items()}
    expected = request_headers(context, request_id)
    for name, value in expected.items():
        if headers.get(name.lower()) != value:
            raise ValueError("encrypted response request binding mismatch")
    if headers.get("content-type", "").split(";", 1)[0] != WIRE_TYPE:
        raise ValueError("encrypted response content type missing")
    original = headers.get("x-qy-content-type", "")
    if not original or "\n" in original or "\r" in original:
        raise ValueError("invalid original response type")
    encoding = headers.get("x-qy-content-encoding", "")
    if encoding not in ("identity", "gzip") or (encoding == "gzip" and not original.startswith("application/json")):
        raise ValueError("invalid encrypted response encoding")
    nonce = unbase64(headers.get("x-qy-nonce", ""))
    key = unbase64(context["key"])
    if len(nonce) != 12 or len(key) != 32 or len(raw) < 16:
        raise ValueError("invalid encrypted response framing")
    aad = "\n".join(["QY-R24-HTTP", "1", context["surface"], context["context_id"],
                      context["key_id"], request_id, method.upper(), target, str(status), original, encoding]).encode()
    plaintext = AESGCM(key).decrypt(nonce, raw, aad)
    if encoding == "gzip":
        with gzip.GzipFile(fileobj=io.BytesIO(plaintext)) as stream:
            plaintext = stream.read(4 * 1024 * 1024 + 1)
        if len(plaintext) > 4 * 1024 * 1024:
            raise ValueError("decompressed response exceeds limit")
    return plaintext, original


def encrypted_client(base_class, reply_class, failure_class):
    class EncryptedClient(base_class):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            self.crypto_lock = threading.Lock()
            self.crypto_context = None
            self.crypto_cookies = None
            self.crypto_rotate = 0.0

        def _initialize(self, key_id=None):
            path = INIT_PATH + ("?key_id=" + urllib.parse.quote(key_id, safe="") if key_id else "")
            reply = super().request("GET", path, extra_headers={"X-QY-Init": "1"})
            data = reply.data
            if (reply.status != 200 or not isinstance(data, dict) or data.get("enabled") is not True
                    or data.get("protocol") != PROTOCOL):
                raise failure_class("R24 encrypted initialization failed")
            if len(unbase64(data.get("key", ""))) != 32:
                raise failure_class("R24 initialization returned an invalid key")
            if data.get("surface") not in ("public", "admin"):
                raise failure_class("R24 initialization returned an invalid surface")
            return data

        def reset_crypto(self):
            with self.crypto_lock:
                self.crypto_context = None
                self.crypto_cookies = None
                self.crypto_rotate = 0.0

        def _context(self):
            with self.crypto_lock:
                if (self.crypto_context is None or self.crypto_cookies != self.cookie_header()
                        or time.monotonic() >= self.crypto_rotate):
                    self.crypto_context = self._initialize()
                    self.crypto_cookies = self.cookie_header()
                    self.crypto_rotate = time.monotonic() + max(0, self.crypto_context["rotate_at"] - self.crypto_context["server_time"] - 2)
                return dict(self.crypto_context)

        def request(self, method, path, body=None, *, origin=None, cookie=None, content_type=None, extra_headers=None):
            if cookie is not None:
                # Security tests need a valid transport for the explicitly forged
                # identity, so transport rejection cannot mask a missing auth gate.
                alternate = type(self)(self.base, self.origin, self.timeout)
                parsed = http.cookies.SimpleCookie()
                parsed.load(cookie)
                alternate.cookies = {name: value.value for name, value in parsed.items()}
                return alternate.request(method, path, body, origin=origin,
                                         content_type=content_type, extra_headers=extra_headers)
            if urllib.parse.urlsplit(path).path == "/healthz":
                return super().request(method, path, body, origin=origin, content_type=content_type,
                                       extra_headers=extra_headers)
            context = self._context()
            request_id = uuid.uuid4().hex
            headers = dict(extra_headers or {})
            headers.update(request_headers(context, request_id))
            headers["X-QY-Accept-Encoding"] = "gzip"
            reply = super().request(method, path, body, origin=origin, content_type=content_type,
                                    extra_headers=headers)
            wire_headers = {key.lower(): value for key, value in reply.headers.items()}
            if reply.status == 0 or method.upper() == "HEAD" or reply.status in (204, 304):
                return reply
            if wire_headers.get("x-qy-protocol-error") == "1":
                self.reset_crypto()
                return reply
            # Origin rejection occurs before initializing any business context.
            if reply.status == 403 and reply.json.get("code") in ("ORIGIN_REJECTED", "CSRF_REJECTED"):
                return reply
            if not reply.raw:
                return reply
            if wire_headers.get("x-qy-response-mode") == "local-image":
                path_only = urllib.parse.urlsplit(path).path
                valid_type = wire_headers.get("content-type", "").split(";", 1)[0] in (
                    "image/jpeg", "image/png", "image/webp", "image/gif")
                bound = all(wire_headers.get(key.lower()) == value for key, value in request_headers(context, request_id).items())
                if not (method.upper() == "GET" and path_only.startswith("/api/v1/media/")
                        and reply.status == 200 and valid_type and bound
                        and wire_headers.get("x-qy-content-encoding") == "identity"):
                    raise failure_class("R24 local image response binding invalid")
                reply.was_local_image = True
                reply.was_encrypted = False
                return reply
            response_key = wire_headers.get("x-qy-key-id")
            if response_key and response_key != context["key_id"]:
                recovered = self._initialize(response_key)
                if recovered["context_id"] != context["context_id"] or recovered["key_id"] != response_key:
                    raise failure_class("R24 response key belongs to another context")
                context = recovered
            parsed = urllib.parse.urlsplit(urllib.parse.urljoin(self.base + "/", path))
            target = parsed.path + ("?" + parsed.query if parsed.query else "")
            try:
                plaintext, original = decrypt_http(reply.raw, reply.headers, reply.status, method,
                                                   target, context, request_id)
            except Exception as exc:
                raise failure_class("R24 response authentication failed") from exc
            decoded_headers = dict(reply.headers)
            decoded_headers["Content-Type"] = original
            decoded_headers["Content-Length"] = str(len(plaintext))
            result = reply_class(reply.status, plaintext, decoded_headers, reply.elapsed, reply.error)
            result.was_encrypted = True
            return result

    return EncryptedClient
