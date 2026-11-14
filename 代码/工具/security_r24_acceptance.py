"""R24 checks run only inside the guarded isolated acceptance suite."""
import concurrent.futures
import datetime
import hashlib
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from response_crypto_acceptance import INIT_PATH, request_headers


class SecurityR24Checks:
    def r24_raw(self, actor, method, path, body=None, **kwargs):
        client = self.clients[actor]
        return super(type(client), client).request(method, path, body, **kwargs)

    def r24_plan(self, actor, items, status=200):
        return self.call(actor, "POST", "/api/v1/media/access-plan", {"items": items}, status=status)

    def r24_functional(self):
        for actor, surface in (("anonymous", "public"), ("admin", "admin")):
            client = self.clients[actor]
            context = client._context()
            self.check("R24 hour key with bounded old-key retention " + surface,
                       0 < context["rotate_at"] - context["server_time"] <= 3600
                       and context["retain_until"] - context["rotate_at"] == 120)
            again = client._initialize()
            self.check("R24 concurrent page initialization preserves current context " + surface,
                       again["context_id"] == context["context_id"] and again["key_id"] == context["key_id"])
            reply = self.call(actor, "GET", "/api/v1/categories")
            self.check("R24 actual JSON body is encrypted " + surface, getattr(reply, "was_encrypted", False))
        media = self.upload("admin")
        self.fixtures["r24_private_media_id"] = media
        private = self.r24_plan("admin", [{"id": media}]).data["items"][0]
        self.check("R24 private media stays on Go", private["mode"] == "go" and private["url"].startswith("/api/v1/media/"))
        reply = self.call("admin", "GET", private["url"])
        self.check("R24 local Go image is the explicit plaintext-image exception",
                   getattr(reply, "was_local_image", False) and reply.raw.startswith(b"\x89PNG\r\n\x1a\n"))
        replay = self.clients["admin"].request("GET", private["url"])
        self.check("R24 consumed Go grant cannot be replayed", replay.status == 403 and replay.json.get("code") == "MEDIA_GRANT_EXPIRED")

        media = self.fixtures["admin_media_id"]
        product = self.admin_product(self.fixtures["platform_product_id"])
        item = {"id": media, "product_id": product["id"], "product_version": product["version"]}
        plan = self.r24_plan("anonymous", [item]).data["items"][0]
        self.check("R24 single public image prefers OSS", plan["mode"] == "oss")
        parsed = urllib.parse.urlsplit(plan["url"])
        self.check("R24 signed URL is a bounded HTTPS OSS object",
                   parsed.scheme == "https" and bool(parsed.hostname) and parsed.hostname.endswith(".aliyuncs.com")
                   and not parsed.username and not parsed.password)
        reference = self.call("anonymous", "GET", f"/api/v1/media/{media}/file")
        self.check("R24 OSS image relayed by Go remains encrypted",
                   getattr(reference, "was_encrypted", False) and not getattr(reference, "was_local_image", False))
        # Sign immediately before use: obtaining a separate Go reference can consume the five-second window.
        plan = self.r24_plan("anonymous", [item]).data["items"][0]
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                return None
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        with opener.open(plan["url"], timeout=10) as response:
            direct = response.read(1024 * 1024)
            self.check("R24 direct OSS bytes equal controlled Go bytes",
                       response.status == 200 and hashlib.sha256(direct).digest() == hashlib.sha256(reference.raw).digest())
        expiry = datetime.datetime.fromisoformat(plan["expires_at"].replace("Z", "+00:00")).timestamp()
        time.sleep(max(0, min(7, expiry - time.time() + 1)))
        try:
            with opener.open(plan["url"], timeout=10) as response:
                expired_status = response.status
        except urllib.error.HTTPError as error:
            expired_status = error.code
            error.close()
        self.check("R24 expired OSS link rejects a fresh request", expired_status == 403)

    def r24_concurrency(self):
        media = self.fixtures["r24_private_media_id"]
        plan = self.r24_plan("admin", [{"id": media}]).data["items"][0]
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
            replies = list(executor.map(lambda _: self.clients["admin"].request("GET", plan["url"]), range(4)))
        self.check("R24 Go grant atomic claim allows one concurrent read",
                   sum(reply.status == 200 for reply in replies) == 1
                   and all(reply.status == 200 or (reply.status == 403 and reply.json.get("code") == "MEDIA_GRANT_EXPIRED") for reply in replies))

    def r24_security(self):
        raw = self.r24_raw("anonymous", "GET", "/api/v1/categories")
        self.check("R24 missing protocol cannot obtain plaintext business data", raw.status == 428 and not raw.data)
        missing_init = self.r24_raw("anonymous", "GET", INIT_PATH)
        self.check("R24 init requires explicit non-embedding request", missing_init.status == 403 and not missing_init.data)
        foreign = self.clients["admin"]._context()
        reply = self.r24_raw("anonymous", "GET", INIT_PATH + "?key_id=" + urllib.parse.quote(foreign["key_id"]),
                             extra_headers={"X-QY-Init": "1"})
        self.check("R24 foreign key cannot be recovered", reply.status in (409, 410) and not reply.data)
        context = self.clients["anonymous"]._context()
        headers = request_headers(context, uuid.uuid4().hex)
        headers["X-QY-Context"] = foreign["context_id"]
        crossed = self.r24_raw("anonymous", "GET", "/api/v1/categories", extra_headers=headers)
        self.check("R24 cross-surface context rejected", crossed.status == 409 and not crossed.data)
        mid = self.fixtures["admin_media_id"]
        self.r24_plan("anonymous", [{"id": mid}] * 2, status=(400, 422))
        self.r24_plan("anonymous", [{"id": str(uuid.uuid4())} for _ in range(13)], status=(400, 413, 422))
        self.r24_plan("anonymous", [{"id": mid, "path": "outside-bucket"}], status=(400, 422))
        self.r24_plan("anonymous", [{"id": self.fixtures["r24_private_media_id"]}], status=(403, 404))
        self.r24_plan("anonymous", [{"id": mid, "product_id": self.fixtures["platform_product_id"],
                                      "product_version": 999999}], status=(404, 409))
