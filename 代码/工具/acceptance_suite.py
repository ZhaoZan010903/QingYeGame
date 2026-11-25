#!/usr/bin/env python3
"""Bounded HTTP acceptance checks for the isolated QingYe acceptance deployment.

Only loopback API targets are accepted. Credentials and cookies stay in memory;
reports and the shared phase-state file contain fixture identifiers only.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import hashlib
import http.cookies
import ipaddress
import json
import math
import os
from pathlib import Path
import struct
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zlib
from admin_accounts_acceptance import AdminAccountsChecks
from recommendation_boost_acceptance import RecommendationBoostChecks


class CheckFailure(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def utcnow():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def local_url(value):
    parsed = urllib.parse.urlsplit(value)
    try:
        local = parsed.hostname == "localhost" or ipaddress.ip_address(parsed.hostname or "").is_loopback
    except ValueError:
        local = False
    if (parsed.scheme != "http" or not local or parsed.username or parsed.password
            or parsed.query or parsed.fragment or parsed.path not in ("", "/")):
        raise argparse.ArgumentTypeError("API and Origin must be plain HTTP loopback origins, without credentials or paths")
    if not parsed.port:
        raise argparse.ArgumentTypeError("An explicit isolated test port is required")
    return value.rstrip("/")


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + ".tmp")
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    try:
        os.chmod(temp, 0o600)
    except OSError:
        pass
    os.replace(temp, path)


def png_bytes(color=(30, 80, 120), width=2, height=2):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    pixels = b"".join(b"\0" + bytes(color) * width for _ in range(height))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(pixels)) + chunk(b"IEND", b""))


def oversized_pixels_png():
    raw = bytearray(png_bytes())
    raw[16:24] = struct.pack(">II", 16001, 16001)
    raw[29:33] = struct.pack(">I", zlib.crc32(raw[12:29]) & 0xFFFFFFFF)
    return bytes(raw)


def percentile(values, percent):
    if not values:
        return None
    ordered = sorted(values)
    return round(ordered[max(0, math.ceil(len(ordered) * percent / 100) - 1)], 2)


def offline_favorite_is_private(items, product_id):
    """Inspect this fixture only; other runs may have public favorites too."""
    if not isinstance(items, list):
        return False
    matching = [item for item in items if isinstance(item, dict) and item.get("id") == product_id]
    if len(matching) != 1:
        return False
    item = matching[0]
    return (item.get("status") == "offline" and item.get("images") == []
            and item.get("image_total") == 0 and item.get("description") in (None, ""))


class Reply:
    def __init__(self, status, raw=b"", headers=None, elapsed=0.0, error=None):
        self.status, self.raw, self.headers, self.elapsed, self.error = status, raw, headers or {}, elapsed, error
        try:
            self.json = json.loads(raw)
        except (ValueError, UnicodeDecodeError):
            self.json = {}

    @property
    def data(self):
        return self.json.get("data") if isinstance(self.json, dict) else None


class Client:
    def __init__(self, base, origin, timeout=20):
        self.base, self.origin, self.timeout = local_url(base), local_url(origin), timeout
        self.cookies = {}
        self.lock = threading.Lock()

    def cookie_header(self):
        with self.lock:
            return "; ".join(f"{key}={value}" for key, value in self.cookies.items())

    def request(self, method, path, body=None, *, origin=None, cookie=None, content_type=None, extra_headers=None):
        # Upload URLs from the server must stay on this same isolated API origin.
        url = urllib.parse.urljoin(self.base + "/", path)
        target = urllib.parse.urlsplit(url)
        if f"{target.scheme}://{target.netloc}" != self.base or not target.path.startswith(("/api/v1/", "/healthz")):
            raise CheckFailure("server returned a URL outside the selected isolated API")
        raw = body if isinstance(body, bytes) else (json.dumps(body, ensure_ascii=False).encode() if body is not None else None)
        headers = {"Content-Type": content_type or "application/json"}
        headers.update(extra_headers or {})
        request_origin = self.origin if origin is None else origin
        if request_origin:
            headers["Origin"] = request_origin
        cookie_value = self.cookie_header() if cookie is None else cookie
        if cookie_value:
            headers["Cookie"] = cookie_value
        request = urllib.request.Request(url, data=raw, headers=headers, method=method)
        start = time.perf_counter()
        try:
            # Do not inherit HTTP_PROXY, and never follow a server redirect.
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
            response = opener.open(request, timeout=self.timeout)
        except urllib.error.HTTPError as exc:
            response = exc
        except (urllib.error.URLError, OSError, TimeoutError) as exc:
            return Reply(0, elapsed=(time.perf_counter() - start) * 1000, error=type(exc).__name__)
        with response:
            payload = response.read(8 * 1024 * 1024)
            for value in response.headers.get_all("Set-Cookie", []):
                parsed = http.cookies.SimpleCookie()
                parsed.load(value)
                with self.lock:
                    for key, morsel in parsed.items():
                        if not morsel.value or morsel["max-age"] == "-1":
                            self.cookies.pop(key, None)
                        else:
                            self.cookies[key] = morsel.value
            return Reply(response.status, payload, dict(response.headers), (time.perf_counter() - start) * 1000)


class Suite(AdminAccountsChecks, RecommendationBoostChecks):
    def __init__(self, args):
        self.args = args
        self.state = json.loads(Path(args.state).read_text(encoding="utf-8")) if Path(args.state).exists() else {}
        expected_targets = {"public": args.public_url, "admin": args.admin_url}
        if self.state.get("targets", expected_targets) != expected_targets:
            raise CheckFailure("state belongs to different isolated API targets")
        self.state["targets"] = expected_targets
        private = json.loads(Path(args.account_file).read_text(encoding="utf-8-sig"))
        accounts = private.get("accounts", [])
        admins = [a for a in accounts if a.get("role") == "admin"]
        members = [a for a in accounts if a.get("role") == "member"]
        if len(admins) != 1 or len(members) < 2:
            raise CheckFailure("account file must contain one admin and at least two members with role, phone and password")
        self.credentials = {"admin": admins[0], "member": members[0], "other": members[1]}
        client_type = Client
        if getattr(args, "response_crypto", False):
            from response_crypto_acceptance import encrypted_client
            client_type = encrypted_client(Client, Reply, CheckFailure)
        self.clients = {
            "admin": client_type(args.admin_url, args.admin_origin),
            "member": client_type(args.public_url, args.public_origin),
            "other": client_type(args.public_url, args.public_origin),
            "anonymous": client_type(args.public_url, args.public_origin),
        }
        self.report = {"phase": args.phase, "started_at": utcnow(), "targets": expected_targets,
                       "checks": [], "metrics": {}, "status": "running"}
        self.fixtures = self.state.setdefault("fixtures", {})

    def check(self, name, ok, **details):
        self.report["checks"].append({"name": name, "ok": bool(ok), **({"details": details} if details else {})})
        if not ok:
            raise CheckFailure(name)

    def expect(self, name, reply, status=200):
        expected = (status,) if isinstance(status, int) else tuple(status)
        self.check(name, reply.status in expected, status=reply.status, expected=list(expected),
                   **({"transport_error": reply.error} if reply.error else {}))
        return reply

    def call(self, actor, method, path, body=None, *, name=None, status=200, **kwargs):
        reply = self.clients[actor].request(method, path, body, **kwargs)
        return self.expect(name or f"{actor} {method} {path.split('?')[0]}", reply, status)

    def login(self, actor):
        entry = self.credentials[actor]
        result = self.call(actor, "POST", "/api/v1/auth/login", {"method": "password", "phone": entry["phone"], "password": entry["password"]}, name=f"{actor} password login")
        self.check(f"{actor} role is correct", result.data.get("role") == ("admin" if actor == "admin" else "member"))
        self.fixtures[f"{actor}_user_id"] = result.data["id"]

    def revoked_identity(self, client, name):
        reply = client.request("GET", "/api/v1/me")
        if getattr(self.args, "response_crypto", False) and reply.status == 409:
            headers = {key.lower(): value for key, value in reply.headers.items()}
            self.check(name + " invalidates the previous crypto context",
                       headers.get("x-qy-protocol-error") == "1"
                       and reply.json.get("code") == "RESPONSE_CONTEXT_CHANGED"
                       and not reply.data)
            # Explicit new read proves business auth also rejects the revoked
            # session; a transport error alone cannot satisfy that assertion.
            reply = client.request("GET", "/api/v1/me")
        self.expect(name, reply, 401)

    def save(self):
        self.state["fixtures"] = self.fixtures
        write_json(self.args.state, self.state)

    def product_input(self, title):
        return {"category_id": self.fixtures["category_id"], "title": title, "trade_type": "sale", "price_unit": "account",
                "price_amount": "100.00", "description": "Isolated acceptance fixture. No customer account data.",
                "attributes": {"server": "acceptance", "gender": "female"}, "request_id": str(uuid.uuid4())}

    def own(self, product_id, actor="member"):
        return self.call(actor, "GET", f"/api/v1/me/products/{product_id}").data

    def admin_product(self, product_id):
        return self.call("admin", "GET", f"/api/v1/admin/products/{product_id}").data

    def public_thumbnail(self, media_id, product):
        path = f"/api/v1/media/{media_id}/file?variant=thumb"
        if self.args.response_crypto and not self.args.no_oss_delivery:
            item = {"id": media_id, "variant": "thumb", "product_id": product["id"], "product_version": product["version"]}
            plan = self.call("anonymous", "POST", "/api/v1/media/access-plan", {"items": [item]}).data["items"][0]
            self.check("visible product thumbnail receives an OSS plan", plan["id"] == media_id and plan["variant"] == "thumb"
                       and plan["mode"] == "oss" and len(plan.get("cache_key", "")) == 64 and plan["mime_type"] == "image/webp")
            denied = self.call("anonymous", "GET", path, status=409)
            self.check("public thumbnail bytes require an OSS plan", denied.json.get("code") == "MEDIA_PLAN_REQUIRED")
        else:
            self.call("anonymous", "GET", path)

    def upload(self, actor="member", raw=None, *, filename="acceptance.png", mime="image/png", complete_status=200, purpose="product"):
        raw = png_bytes() if raw is None else raw
        ticket = self.call(actor, "POST", "/api/v1/uploads", {"filename": filename, "mime_type": mime, "byte_size": len(raw), "purpose": purpose}, status=(200, 201)).data
        self.call(actor, "PUT", ticket["upload_url"], raw, content_type=mime, name=f"{actor} upload bytes")
        result = self.call(actor, "POST", f"/api/v1/uploads/{ticket['media_id']}/complete", status=complete_status)
        if complete_status == 200:
            self.check("uploaded image is ready", result.data.get("status") == "ready")
        return ticket["media_id"]

    def submit_approve(self, product_id):
        p = self.own(product_id)
        submitted = self.call("member", "POST", f"/api/v1/me/products/{product_id}/submit", {"expected_version": p["version"]}).data
        review = {"expected_version": submitted["version"], "decision": "approve"}
        if submitted.get("trade_type") == "rent":
            review["rental_category"] = submitted.get("rental_category") or "hour_day"
        approved = self.call("admin", "POST", f"/api/v1/admin/products/{product_id}/review", review).data
        return self.wait_published(product_id, approved)

    def wait_published(self, product_id, product):
        # R20 publication confirms storage asynchronously. Poll only the read API;
        # never replay an approval or expose an item before its server state settles.
        deadline = time.monotonic() + 45
        while product.get("status") == "publishing" and time.monotonic() < deadline:
            time.sleep(.25)
            reply = self.clients["admin"].request("GET", f"/api/v1/admin/products/{product_id}")
            self.expect("publication progress remains readable", reply)
            product = reply.data
        self.check("publication reaches confirmed published state", product.get("status") == "published")
        return product

    def functional(self):
        self.state["functional_complete"] = False
        self.state.pop("concurrency_complete", None)
        self.state.pop("security_complete", None)
        self.fixtures = {key: value for key, value in self.fixtures.items() if key.endswith("_user_id")}
        prefix = "acceptance-" + uuid.uuid4().hex[:10]
        self.state["run_id"] = prefix
        self.fixtures["category_id"] = prefix
        dictionaries = self.call("admin", "GET", "/api/v1/admin/dictionaries").data
        server = next(group for group in dictionaries if group["key"] == "server")
        options = [dict(option) for option in server["options"]]
        existing = next((option for option in options if option["value"] == "acceptance"), None)
        if existing:
            existing["enabled"] = True
        else:
            options.append({"value": "acceptance", "label": "Acceptance", "enabled": True, "sort_order": 999})
        self.call("admin", "PUT", "/api/v1/admin/dictionaries/server", {"expected_version": server["version"], "options": options})
        category = {"code": prefix, "name": prefix, "trade_type": "all", "price_units": ["account", "day", "period", "unknown"],
                    "filter_config": [{"key": "server", "label": "Test region", "multiple": False,
                                       "options": [{"value": "acceptance", "label": "Acceptance"}]}], "sort_order": 999, "enabled": True}
        self.call("admin", "POST", "/api/v1/admin/categories", category)
        payload = self.product_input(prefix + " member listing")
        product = self.call("member", "POST", "/api/v1/me/products", payload).data
        pid = product["id"]
        self.fixtures["product_id"] = pid
        repeated = self.call("member", "POST", "/api/v1/me/products", payload).data
        self.check("draft creation is idempotent", repeated["id"] == pid)
        self.call("other", "GET", f"/api/v1/me/products/{pid}", status=404)
        self.call("anonymous", "GET", f"/api/v1/products/{pid}", status=404)
        first, second = self.upload(), self.upload(raw=png_bytes((50, 130, 80)))
        self.fixtures["media_id"] = second
        self.call("anonymous", "GET", f"/api/v1/media/{second}/file", status=404)
        self.call("other", "GET", f"/api/v1/media/{second}/file", status=404)
        self.call("member", "GET", f"/api/v1/media/{second}/file")
        append = {"media_ids": [first, second], "expected_version": product["version"], "request_id": str(uuid.uuid4())}
        product = self.call("member", "POST", f"/api/v1/me/products/{pid}/images", append).data
        repeated = self.call("member", "POST", f"/api/v1/me/products/{pid}/images", append).data
        self.check("image append replay preserves version and count", repeated["version"] == product["version"] and repeated["image_total"] == 2)
        self.call("member", "PATCH", f"/api/v1/me/products/{pid}/images/{second}", {"expected_version": product["version"], "before_media_id": first})
        product = self.own(pid)
        images = self.call("member", "GET", f"/api/v1/me/products/{pid}/images?limit=1&expected_version={product['version']}")
        self.check("image move and pagination", images.data[0]["id"] == second and images.json.get("next_cursor") is not None)
        self.call("member", "DELETE", f"/api/v1/me/products/{pid}/images/{first}", {"expected_version": product["version"]})
        product = self.own(pid)
        pending = self.call("member", "POST", f"/api/v1/me/products/{pid}/submit", {"expected_version": product["version"]}).data
        rejected = self.call("admin", "POST", f"/api/v1/admin/products/{pid}/review", {"expected_version": pending["version"], "decision": "reject", "reason": "Acceptance rejection fixture"}).data
        self.check("review rejection is visible to owner", rejected["status"] == "rejected" and bool(self.own(pid).get("review_note")))
        edit = dict(payload, title=prefix + " corrected", expected_version=rejected["version"])
        self.call("member", "PATCH", f"/api/v1/me/products/{pid}", edit)
        product = self.submit_approve(pid)
        self.call("anonymous", "GET", f"/api/v1/products/{pid}")
        self.public_thumbnail(second, product)
        self.call("admin", "POST", f"/api/v1/admin/products/{pid}/publish", {"expected_version": product["version"]}, status=409)
        for _ in range(2):
            self.call("other", "PUT", f"/api/v1/me/favorites/{pid}")
        other_favs = self.call("other", "GET", "/api/v1/me/favorites").data
        member_favs = self.call("member", "GET", "/api/v1/me/favorites").data
        self.check("favorites are idempotent and isolated", sum(p["id"] == pid for p in other_favs) == 1 and all(p["id"] != pid for p in member_favs))
        self.call("other", "DELETE", f"/api/v1/me/favorites/{pid}")
        self.call("other", "PUT", f"/api/v1/me/favorites/{pid}")
        self.call("member", "PATCH", f"/api/v1/me/products/{pid}", dict(payload, expected_version=product["version"]))
        self.call("anonymous", "GET", f"/api/v1/products/{pid}", status=404)
        self.call("anonymous", "GET", f"/api/v1/media/{second}/file?variant=thumb", status=404)
        self.submit_approve(pid)
        product = self.own(pid)
        self.call("member", "POST", f"/api/v1/me/products/{pid}/withdraw", {"expected_version": product["version"]})
        self.call("anonymous", "GET", f"/api/v1/products/{pid}", status=410)
        product = self.submit_approve(pid)
        self.call("admin", "POST", f"/api/v1/admin/products/{pid}/status", {"expected_version": product["version"], "action": "offline", "reason": "Acceptance offline fixture"})
        self.call("anonymous", "GET", f"/api/v1/products/{pid}", status=410)
        self.submit_approve(pid)
        platform = self.call("admin", "POST", "/api/v1/admin/products", self.product_input(prefix + " platform")).data
        platform_image = self.upload("admin")
        if self.args.response_crypto:
            # The source upload is sanitized before publication. Pin those actual
            # authorized bytes so OSS is compared with an independent reference.
            reference = self.call("admin", "GET", f"/api/v1/media/{platform_image}/file")
            self.check("draft fixture yields sanitized PNG bytes", reference.raw.startswith(b"\x89PNG\r\n\x1a\n"))
            self.fixtures["r24_platform_image_sha256"] = hashlib.sha256(reference.raw).hexdigest()
        platform = self.call("admin", "POST", f"/api/v1/admin/products/{platform['id']}/images", {"media_ids": [platform_image], "expected_version": platform["version"], "request_id": str(uuid.uuid4())}).data
        platform = self.call("admin", "POST", f"/api/v1/admin/products/{platform['id']}/publish", {"expected_version": platform["version"]}).data
        platform = self.wait_published(platform['id'], platform)
        self.call("anonymous", "GET", f"/api/v1/products/{platform['id']}")
        self.fixtures["platform_product_id"], self.fixtures["admin_media_id"] = platform["id"], platform_image
        self.rental_quotes_workflow(prefix)
        contact_body = {"name": prefix, "purpose": ["general"], "channel": "wechat", "contact_value": "acceptance-only", "hours": "test", "enabled": True}
        contact = self.call("admin", "POST", "/api/v1/admin/contacts", contact_body).data
        self.fixtures["contact_id"] = contact["id"]
        self.check("contact is publicly configurable", any(c["id"] == contact["id"] for c in self.call("anonymous", "GET", "/api/v1/contacts").data))
        self.call("admin", "PUT", f"/api/v1/admin/contacts/{contact['id']}", dict(contact_body, enabled=False))
        self.check("disabled contact is hidden", all(c["id"] != contact["id"] for c in self.call("anonymous", "GET", "/api/v1/contacts").data))
        slug = prefix + "-about"
        page = {"title": prefix, "body": "Isolated acceptance page", "published": False}
        self.call("admin", "PUT", f"/api/v1/admin/pages/{slug}", page)
        self.call("anonymous", "GET", f"/api/v1/pages/{slug}", status=404)
        self.call("admin", "PUT", f"/api/v1/admin/pages/{slug}", dict(page, published=True))
        self.call("anonymous", "GET", f"/api/v1/pages/{slug}")
        self.call("admin", "PUT", f"/api/v1/admin/categories/{prefix}", dict(category, enabled=False))
        self.call("anonymous", "GET", f"/api/v1/products/{pid}", status=404)
        self.call("anonymous", "GET", f"/api/v1/media/{second}/file", status=404)
        self.call("admin", "PUT", f"/api/v1/admin/categories/{prefix}", category)
        self.call("anonymous", "GET", f"/api/v1/products/{pid}")
        presentation = {"editorial": {"title": "Acceptance", "description": "Editable", "intervalMs": 5000}, "sections": [{"source": "hot", "title": "Hot", "description": "", "limit": 3}, {"source": "newest", "title": "New", "description": "", "limit": 3}], "shortcuts": [], "publishing": {"title": "Publish", "description": "Test", "action": "Submit"}}
        self.call("admin", "PUT", "/api/v1/admin/settings/home_presentation", {"value": presentation})
        home = self.call("anonymous", "GET", "/api/v1/home").data
        self.check("each home section has unique products; rankings remain independent", all(
            len(items) == len({p["id"] for p in items})
            for items in (home.get(key, []) for key in ("featured", "hot", "recommended", "newest"))))
        other_id = self.fixtures["other_user_id"]
        self.call("admin", "PATCH", f"/api/v1/admin/users/{other_id}", {"enabled": False})
        try:
            self.revoked_identity(self.clients["other"], "disabled member session is rejected")
        finally:
            self.call("admin", "PATCH", f"/api/v1/admin/users/{other_id}", {"enabled": True})
        self.call("other", "GET", "/api/v1/me", status=401)
        self.login("other")
        self.call("other", "POST", "/api/v1/auth/logout")
        self.call("other", "GET", "/api/v1/me", status=401)
        self.login("other")
        self.dashboard_functional()
        self.purge_functional()
        self.r22_functional()
        self.r23_functional()
        self.state["functional_complete"] = True

    def purge_fixture(self, name, image=False):
        product = self.call("admin", "POST", "/api/v1/admin/products", self.product_input(self.state["run_id"] + " " + name)).data
        mid = None
        if image:
            mid = self.upload("admin", raw=png_bytes((71, 124, 194)))
            product = self.call("admin", "POST", f"/api/v1/admin/products/{product['id']}/images",
                                {"media_ids": [mid], "expected_version": product["version"], "request_id": str(uuid.uuid4())}).data
            product = self.call("admin", "POST", f"/api/v1/admin/products/{product['id']}/publish", {"expected_version": product["version"]}).data
            product = self.wait_published(product['id'], product)
            self.fixtures['purged_oss_media_id'] = mid
        result = self.call("admin", "DELETE", f"/api/v1/admin/products/{product['id']}",
                           {"expected_version": product["version"], "reason": "Isolated purge acceptance fixture"}).data
        return product["id"], result["version"], mid

    def purge_functional(self):
        # All destructive fixture checks are confined to the suite's isolated ports.
        pid, version, mid = self.purge_fixture("permanent deletion", image=True)
        path = f"/api/v1/admin/products/{pid}/purge"
        body = {"expected_version": version, "confirmation": "永久删除", "reason": "Isolated acceptance", "request_id": str(uuid.uuid4())}
        self.call("admin", "POST", path, dict(body, confirmation="delete"), status=(400, 422))
        before = self.call("admin", "GET", "/api/v1/admin/products/trash/preview").data
        self.check("purge preview counts current trashed products and images", before["total"] >= 1 and before["image_count"] >= 1 and len(before["snapshot"]) == 64)
        self.call("admin", "POST", path, dict(body, expected_version=version-1), status=409)
        deleted = self.call("admin", "POST", path, body).data
        replay = self.call("admin", "POST", path, body).data
        self.check("permanent deletion result is replayed without another deletion", deleted == replay and deleted["purged_count"] == 1)
        self.call("admin", "POST", path, dict(body, reason="Different request body"), status=409)
        self.call("admin", "POST", f"/api/v1/admin/products/{pid}/restore", {"expected_version": version+1, "reason": "Must not restore"}, status=404)
        self.call("admin", "GET", f"/api/v1/admin/products/{pid}", status=404)
        self.call("admin", "GET", f"/api/v1/media/{mid}/file", status=404)
        first, first_version, _ = self.purge_fixture("batch first")
        preview = self.call("admin", "GET", "/api/v1/admin/products/trash/preview").data
        second, second_version, _ = self.purge_fixture("batch added after preview")
        batch = {"snapshot": preview["snapshot"], "confirmation": "清空回收站", "reason": "Isolated batch acceptance", "request_id": str(uuid.uuid4())}
        self.call("admin", "POST", "/api/v1/admin/products/trash/purge", batch, status=409)
        restored = self.call("admin", "POST", f"/api/v1/admin/products/{first}/restore", {"expected_version": first_version, "reason": "Verify conflict did not delete"}).data
        self.check("restored product remains available after stale clear request", self.admin_product(first)["status"] == "draft")
        self.call("admin", "DELETE", f"/api/v1/admin/products/{first}", {"expected_version": restored["version"], "reason": "Return isolated fixture"})
        fresh = self.call("admin", "GET", "/api/v1/admin/products/trash/preview").data
        batch = dict(batch, snapshot=fresh["snapshot"], request_id=str(uuid.uuid4()))
        cleared = self.call("admin", "POST", "/api/v1/admin/products/trash/purge", batch).data
        self.check("clear removes exactly the confirmed full recycle bin", cleared["purged_count"] == fresh["total"] and fresh["total"] >= 2)
        self.check("clear result survives response retry", self.call("admin", "POST", "/api/v1/admin/products/trash/purge", batch).data == cleared)
        self.check("recycle bin is empty after clear", self.call("admin", "GET", "/api/v1/admin/products/trash/preview").data["total"] == 0)
        self.call("admin", "GET", f"/api/v1/admin/products/{second}", status=404)
        self.call("anonymous", "GET", f"/api/v1/products/{self.fixtures['product_id']}")

    def purge_concurrency(self):
        pid, version, _ = self.purge_fixture("parallel idempotent purge")
        path = f"/api/v1/admin/products/{pid}/purge"
        body = {"expected_version": version, "confirmation": "永久删除", "reason": "Concurrent acceptance", "request_id": str(uuid.uuid4())}
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            replies = list(executor.map(lambda _: self.clients["admin"].request("POST", path, body), range(16)))
        self.check("concurrent purge retries all return the same committed result", all(r.status == 200 and r.data == replies[0].data for r in replies) and replies[0].data["purged_count"] == 1)

    def purge_security(self):
        preview = "/api/v1/admin/products/trash/preview"
        purge = "/api/v1/admin/products/trash/purge"
        admin = self.clients["admin"]
        self.expect("trash preview requires administrator session", admin.request("GET", preview, cookie=""), 401)
        self.call("member", "GET", preview, status=404)
        self.call("member", "POST", purge, {}, status=404)
        for origin in (self.args.public_origin, "null", ""):
            self.expect("permanent deletion rejects wrong or missing origin", admin.request("POST", purge, {}, origin=origin), 403)
        pid = self.fixtures["platform_product_id"]
        product = self.admin_product(pid)
        body = {"expected_version": product["version"], "confirmation": "永久删除", "reason": "Must reject live listing", "request_id": str(uuid.uuid4())}
        self.call("admin", "POST", f"/api/v1/admin/products/{pid}/purge", body, status=409)
        self.check("rejected purge keeps a normal product untouched", self.admin_product(pid)["version"] == product["version"])

    def dashboard_contract(self, reply, days=7, rank_by="views"):
        """Check one snapshot without logging titles, user details or raw bodies."""
        data = reply.data
        tag = f"dashboard {days}d {rank_by}"
        statuses = {"draft", "pending", "publishing", "published", "rejected", "rejected_final", "offline", "sold", "rented"}
        metrics = {"views", "favorites", "consultations"}
        nonnegative = lambda values: all(type(value) is int and value >= 0 for value in values)
        self.check(tag + " has only the approved aggregate sections", isinstance(data, dict) and set(data) == {
            "generated_at", "timezone", "range", "products", "members", "todos", "engagement", "trend", "ranking"})
        shapes = {
            "range": {"days", "start_date", "end_date", "start_at", "end_at"},
            "products": {"total", "visible", "sale", "rent", "trash", "statuses"},
            "members": {"total", "enabled", "disabled", "new_in_period"},
            "todos": {"pending_products", "publishing_products", "pending_nicknames", "waiting_conversations"},
            "engagement": metrics, "ranking": {"by", "limit", "items"},
        }
        self.check(tag + " has strictly scoped fields", all(isinstance(data[key], dict) and set(data[key]) == keys for key, keys in shapes.items())
                   and isinstance(data["products"]["statuses"], dict) and set(data["products"]["statuses"]) == statuses)
        window, products, members, todos = (data[key] for key in ("range", "products", "members", "todos"))
        start, end = (dt.date.fromisoformat(window[key]) for key in ("start_date", "end_date"))
        generated = dt.datetime.fromisoformat(data["generated_at"].replace("Z", "+00:00"))
        start_at, end_at = (dt.datetime.fromisoformat(window[key].replace("Z", "+00:00")) for key in ("start_at", "end_at"))
        beijing = dt.timezone(dt.timedelta(hours=8))
        self.check(tag + " uses inclusive Beijing calendar dates and one snapshot clock", data["timezone"] == "Asia/Shanghai"
                   and window["days"] == days and (end - start).days == days - 1
                   and all(value.tzinfo is not None for value in (generated, start_at, end_at))
                   and start_at == dt.datetime.combine(start, dt.time.min, beijing)
                   and end_at == generated and generated.astimezone(beijing).date() == end)
        self.check(tag + " counts are nonnegative integers", nonnegative([value for key, value in products.items() if key != "statuses"])
                   and all(nonnegative(values.values()) for values in (products["statuses"], members, todos, data["engagement"])))
        self.check(tag + " inventory membership and todo counts reconcile", products["sale"] + products["rent"] == products["total"]
                   and sum(products["statuses"].values()) == products["total"] and products["visible"] <= products["statuses"]["published"]
                   and members["enabled"] + members["disabled"] == members["total"] and members["new_in_period"] <= members["total"]
                   and todos["pending_products"] == products["statuses"]["pending"] and todos["publishing_products"] == products["statuses"]["publishing"])
        trend = data["trend"]
        self.check(tag + " contains every ordered day including zero buckets", isinstance(trend, list) and len(trend) == days
                   and all(isinstance(row, dict) and set(row) == metrics | {"date", "new_members"}
                           and row["date"] == (start + dt.timedelta(days=index)).isoformat()
                           and nonnegative(row[key] for key in metrics | {"new_members"}) for index, row in enumerate(trend)))
        self.check(tag + " daily series sum to the selected-period totals", all(sum(row[key] for row in trend) == data["engagement"][key] for key in metrics)
                   and sum(row["new_members"] for row in trend) == members["new_in_period"])
        ranking, items = data["ranking"], data["ranking"]["items"]
        self.check(tag + " ranking is bounded and contains no private product fields", ranking["by"] == rank_by and ranking["limit"] == 10
                   and isinstance(items, list) and len(items) <= 10
                   and all(isinstance(row, dict) and set(row) == metrics | {"id", "product_code", "title", "trade_type", "rental_category", "status"}
                           and all(isinstance(row[key], str) and bool(row[key]) for key in ("id", "product_code", "title"))
                           and row["trade_type"] in ("sale", "rent") and row["status"] in statuses
                           and nonnegative(row[key] for key in metrics) and row[rank_by] > 0 for row in items))
        self.check(tag + " ranking is unique ordered and bounded by its aggregate", len({row["id"] for row in items}) == len(items)
                   and items == sorted(items, key=lambda row: (-row[rank_by], row["product_code"], row["id"]))
                   and all(sum(row[key] for row in items) <= data["engagement"][key] for key in metrics)
                   and (len(items) == 10 or sum(row[rank_by] for row in items) == data["engagement"][rank_by]))
        self.check(tag + " response cannot be cached", "no-store" in reply.headers.get("Cache-Control", "").lower())
        return data

    def dashboard_functional(self):
        path = "/api/v1/admin/dashboard"
        default = self.dashboard_contract(self.call("admin", "GET", path))
        snapshots = {}
        for days in (7, 30):
            for rank_by in ("views", "favorites", "consultations"):
                snapshots[days, rank_by] = self.dashboard_contract(self.call("admin", "GET", f"{path}?days={days}&rank_by={rank_by}"), days, rank_by)
        current = snapshots[7, "views"]
        self.check("dashboard defaults select seven days and views", default["range"]["days"] == 7 and default["ranking"]["by"] == "views")
        self.check("dashboard ranking selection does not change its aggregate scope", all(
            all(value[key] == snapshots[days, "views"][key] for key in ("products", "members", "todos", "engagement", "trend"))
            for (days, _), value in snapshots.items()))
        self.check("dashboard thirty-day totals include the seven-day window", all(
            snapshots[30, "views"]["engagement"][key] >= current["engagement"][key] for key in current["engagement"])
            and snapshots[30, "views"]["members"]["new_in_period"] >= current["members"]["new_in_period"])
        for suffix, expected in (("", current["products"]["total"]), ("&trash=1", current["products"]["trash"]),
                                 ("&trade_type=sale", current["products"]["sale"]), ("&trade_type=rent", current["products"]["rent"]),
                                 ("&status=pending", current["todos"]["pending_products"]), ("&status=publishing", current["todos"]["publishing_products"])):
            listed = self.call("admin", "GET", "/api/v1/admin/products?limit=20" + suffix)
            self.check("dashboard inventory and queue counts match filtered resource lists" + (suffix or " all"), listed.json.get("total") == expected)
        nicknames = self.call("admin", "GET", "/api/v1/admin/nickname-requests?status=pending&page=1&limit=20")
        self.check("dashboard nickname todo matches the pending review queue", nicknames.json.get("pagination", {}).get("total") == current["todos"]["pending_nicknames"])

    def dashboard_security(self):
        path = "/api/v1/admin/dashboard"
        admin = self.clients["admin"]
        self.call("anonymous", "GET", path, status=404, name="dashboard is absent from anonymous public API")
        self.call("member", "GET", path, status=404, name="dashboard is absent from authenticated public API")
        self.expect("dashboard rejects anonymous admin-surface access", admin.request("GET", path, cookie=""), 401)
        self.expect("dashboard rejects public session cookie", admin.request("GET", path, cookie=self.clients["member"].cookie_header()), 401)
        self.expect("dashboard rejects renamed member session", admin.request("GET", path, cookie="qingye_admin_session=" + self.clients["member"].cookies.get("qingye_session", "")), 401)
        for query in ("days=0", "days=1", "days=8", "days=365", "days=-7", "days=07", "days=7.0", "days=", "rank_by=", "rank_by=price",
                      "days=7&days=30", "rank_by=views&rank_by=favorites", "unknown=value", "days=%zz", "days=7;rank_by=views",
                      "days=7%3BDROP%20TABLE%20products", "rank_by=views%27%20OR%201%3D1--"):
            reply = self.call("admin", "GET", path + "?" + query, status=400, name="dashboard rejects unsupported duplicate empty or injected query")
            self.check("dashboard invalid query has a stable public error code", reply.json.get("code") == "INVALID_DASHBOARD_QUERY")

    def concurrency(self):
        self.state["concurrency_complete"] = False
        self.state.pop("security_complete", None)
        pid = self.fixtures["product_id"]
        paths = ["/api/v1/products?limit=20", f"/api/v1/products/{pid}", f"/api/v1/products/{pid}/images?limit=20", "/api/v1/categories", "/api/v1/contacts", "/api/v1/home"]
        for workers, count in ((20, 300), (50, 500)):
            start = time.perf_counter()
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                responses = list(executor.map(lambda i: self.clients["anonymous"].request("GET", paths[i % len(paths)]), range(count)))
            durations = [r.elapsed for r in responses]
            failures = [r for r in responses if r.status != 200]
            self.report["metrics"][f"read_{workers}_concurrent"] = {"concurrency": workers, "requests": count, "elapsed_seconds": round(time.perf_counter() - start, 3), "p50_ms": percentile(durations, 50), "p95_ms": percentile(durations, 95), "p99_ms": percentile(durations, 99), "errors": len(failures), "error_statuses": sorted(set(r.status for r in failures))}
            self.check(f"{workers} concurrent reads complete without errors", not failures, requests=count)
        start = time.perf_counter()
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            responses = list(executor.map(lambda _: self.clients["admin"].request("GET", "/api/v1/admin/dashboard?days=30&rank_by=views"), range(32)))
        durations = [reply.elapsed for reply in responses]
        failures = [reply for reply in responses if reply.status != 200]
        self.report["metrics"]["dashboard_read_8_concurrent"] = {"concurrency": 8, "requests": 32, "elapsed_seconds": round(time.perf_counter() - start, 3),
            "p50_ms": percentile(durations, 50), "p95_ms": percentile(durations, 95), "p99_ms": percentile(durations, 99), "errors": len(failures),
            "error_statuses": sorted(set(reply.status for reply in failures))}
        self.check("bounded parallel administrator dashboard reads complete without errors", not failures)
        baseline = self.dashboard_contract(responses[0], 30, "views")
        self.check("parallel dashboard reads preserve the same aggregate and ranking", all(isinstance(reply.data, dict)
            and all(reply.data.get(key) == baseline[key] for key in ("timezone", "products", "members", "todos", "engagement", "trend", "ranking")) for reply in responses))
        payload = self.product_input(self.state["run_id"] + " concurrent")
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
            replies = list(executor.map(lambda _: self.clients["member"].request("POST", "/api/v1/me/products", payload), range(20)))
        self.check("20 concurrent identical create requests succeed", all(r.status == 200 for r in replies))
        ids = {r.data["id"] for r in replies}
        self.check("concurrent create has exactly one product ID", len(ids) == 1)
        race_id = next(iter(ids)); self.fixtures["concurrency_product_id"] = race_id
        version = self.own(race_id)["version"]
        def update(index):
            return self.clients["member"].request("PATCH", f"/api/v1/me/products/{race_id}", dict(payload, title=f"Concurrency winner {index}", expected_version=version))
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
            replies = list(executor.map(update, range(20)))
        statuses = [r.status for r in replies]
        self.check("same version admits exactly one writer", statuses.count(200) == 1 and statuses.count(409) == 19, successes=statuses.count(200), conflicts=statuses.count(409))
        product = self.own(race_id)
        self.check("concurrent edits increment once", product["version"] == version + 1)
        image = self.upload()
        append = {"media_ids": [image], "expected_version": product["version"], "request_id": str(uuid.uuid4())}
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
            replies = list(executor.map(lambda _: self.clients["member"].request("POST", f"/api/v1/me/products/{race_id}/images", append), range(20)))
        self.check("concurrent image append is idempotent", all(r.status == 200 for r in replies) and self.own(race_id)["image_total"] == 1)
        product = self.own(race_id)
        pending = self.call("member", "POST", f"/api/v1/me/products/{race_id}/submit", {"expected_version": product["version"]}).data
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
            replies = list(executor.map(lambda _: self.clients["admin"].request("POST", f"/api/v1/admin/products/{race_id}/review", {"expected_version": pending["version"], "decision": "approve"}), range(20)))
        statuses = [r.status for r in replies]
        self.check("concurrent reviews publish once", statuses.count(200) == 1 and statuses.count(409) == 19, successes=statuses.count(200), conflicts=statuses.count(409))
        self.purge_concurrency()
        self.r22_concurrency()
        self.r23_concurrency()
        self.state["concurrency_complete"] = True

    def rental_quotes_workflow(self, prefix):
        # This suite only permits its dedicated loopback acceptance services.
        payload = dict(self.product_input(prefix + " rental quotes"), trade_type="rent", price_unit="period",
                       rental_quotes=[{"unit": "period", "amount": "90", "label": "长包"},
                                      {"unit": "period", "amount": "20", "label": "包夜"},
                                      {"unit": "period", "amount": "8", "label": "夜间", "start_time": "22:00", "end_time": "07:00", "billing_unit": "hour"},
                                      {"unit": "day", "amount": "25"}])
        self.call("member", "POST", "/api/v1/me/products", dict(payload, rental_category=""), status=403)
        product = self.call("member", "POST", "/api/v1/me/products", payload).data
        pid = product["id"]
        image = self.upload()
        product = self.call("member", "POST", f"/api/v1/me/products/{pid}/images", {"media_ids": [image], "expected_version": product["version"], "request_id": str(uuid.uuid4())}).data
        pending = self.call("member", "POST", f"/api/v1/me/products/{pid}/submit", {"expected_version": product["version"]}).data
        review = {"expected_version": pending["version"], "decision": "approve"}
        self.call("admin", "POST", f"/api/v1/admin/products/{pid}/review", review, status=422)
        published = self.call("admin", "POST", f"/api/v1/admin/products/{pid}/review", dict(review, rental_category="week_month")).data
        product = self.wait_published(pid, published)
        self.check("rental category is assigned by the approving administrator", product.get("rental_category") == "week_month")
        public = self.call("anonymous", "GET", f"/api/v1/products/{pid}").data
        self.check("all quote details remain public", len(public.get("rental_quotes", [])) == 4 and public["rental_quotes"][2].get("billing_unit") == "hour")
        for basis, price in (("period", "20"), ("hour", "8")):
            rows = self.call("anonymous", "GET", f"/api/v1/products?q={prefix}&price_unit=period&period_basis={basis}&sort=price_asc&max_price=30").data
            matches = [item for item in rows if item["id"] == pid]
            self.check(f"period search compares the minimum {basis} price", len(matches) == 1 and matches[0].get("price_amount") == price)
        self.call("member", "PATCH", f"/api/v1/me/products/{pid}", dict(payload, expected_version=product["version"], rental_category="hour_day"), status=403)
        self.fixtures["rental_product_id"] = pid

    def security(self):
        self.state["security_complete"] = False
        public, admin = self.clients["member"], self.clients["admin"]
        admin_entry, member_entry = self.credentials["admin"], self.credentials["member"]
        self.expect("public login rejects administrator role", self.clients["anonymous"].request("POST", "/api/v1/auth/login", {"method": "password", "phone": admin_entry["phone"], "password": admin_entry["password"]}), 401)
        self.expect("administrator login rejects member role", admin.request("POST", "/api/v1/auth/login", {"method": "password", "phone": member_entry["phone"], "password": member_entry["password"]}), 401)
        member_token = public.cookies.get("qingye_session", "")
        admin_token = admin.cookies.get("qingye_admin_session", "")
        self.check("independent session cookie names", bool(member_token) and bool(admin_token) and member_token != admin_token)
        self.expect("renamed member token rejected by admin", admin.request("GET", "/api/v1/me", cookie="qingye_admin_session=" + member_token), 401)
        self.expect("renamed admin token rejected by public", public.request("GET", "/api/v1/me", cookie="qingye_session=" + admin_token), 401)
        both = public.cookie_header() + "; " + admin.cookie_header()
        pub_identity = self.expect("same-host public cookie selection", public.request("GET", "/api/v1/me", cookie=both)).data
        adm_identity = self.expect("same-host admin cookie selection", admin.request("GET", "/api/v1/me", cookie=both)).data
        self.check("two cookies cannot exchange identities", pub_identity["id"] == self.fixtures["member_user_id"] and adm_identity["id"] == self.fixtures["admin_user_id"])
        self.call("member", "GET", "/api/v1/admin/products", status=404)
        self.expect("admin API has no member writes", admin.request("POST", "/api/v1/me/products", self.product_input("forbidden")), 404)
        self.expect("admin media requires administrator", admin.request("GET", "/api/v1/media/not-existing/file", cookie=""), 401)
        self.expect("admin public previews require administrator", admin.request("GET", "/api/v1/categories", cookie=""), 401)
        for origin in (self.args.public_origin, "null", ""):
            self.expect("admin rejects wrong or missing write origin", admin.request("POST", "/api/v1/admin/categories", {}, origin=origin), 403)
        self.expect("public rejects administrator origin", public.request("PATCH", "/api/v1/me", {"nickname": "forged"}, origin=self.args.admin_origin), 403)
        self.call("member", "POST", "/api/v1/me/products", {"owner_id": self.fixtures["other_user_id"], "source": "platform", "status": "published"}, status=422)
        self.call("admin", "PUT", "/api/v1/admin/settings/session_secret", {"value": "not-allowed"}, status=422)
        self.call("admin", "PATCH", f"/api/v1/admin/users/{self.fixtures['admin_user_id']}", {"enabled": False}, status=422)
        injection = urllib.parse.urlencode({"q": "' OR 1=1; --", "limit": 20})
        search = self.call("anonymous", "GET", "/api/v1/products?" + injection)
        self.check("SQL metacharacters remain a search value", isinstance(search.data, list) and not search.data)
        for path in ("/api/v1/media/%2e%2e/file", "/api/v1/media/%2e%2e%2fetc%2fpasswd/file", "/api/v1/media/../files/etc/passwd"):
            self.call("anonymous", "GET", path, name="path traversal rejected", status=(301, 400, 404))
        for body in ({"filename": "../x.png", "mime_type": "image/png", "byte_size": 50}, {"filename": "x.svg", "mime_type": "image/svg+xml", "byte_size": 50}, {"filename": "x.png", "mime_type": "image/png", "byte_size": self.args.max_upload_bytes + 1}):
            self.call("member", "POST", "/api/v1/uploads", body, name="invalid upload ticket rejected", status=(413, 422))
        self.upload(raw=b"<html>not an image</html>", complete_status=422)
        self.upload(raw=png_bytes(), filename="forged.jpg", mime="image/jpeg", complete_status=422)
        self.upload(raw=oversized_pixels_png(), complete_status=422)
        private_media = self.upload(raw=png_bytes((160, 25, 70)))
        self.call("other", "GET", f"/api/v1/media/{private_media}/file", status=404)
        self.call("anonymous", "GET", f"/api/v1/media/{private_media}/file?variant=thumb", status=404)
        ticket = self.call("admin", "POST", "/api/v1/uploads", {"filename": "admin-only.png", "mime_type": "image/png", "byte_size": len(png_bytes())}, status=(200, 201)).data
        self.expect("admin upload ticket cannot cross into public API", public.request("PUT", ticket["upload_url"], png_bytes(), content_type="image/png"), 403)
        pid, mid = self.fixtures["product_id"], self.fixtures["media_id"]
        product = self.own(pid)
        self.public_thumbnail(mid, product)
        self.call("member", "POST", f"/api/v1/me/products/{pid}/withdraw", {"expected_version": product["version"]})
        self.call("anonymous", "GET", f"/api/v1/products/{pid}", status=410)
        self.call("anonymous", "GET", f"/api/v1/media/{mid}/file", status=404)
        self.call("anonymous", "GET", f"/api/v1/media/{mid}/file?variant=thumb", status=404)
        favorites = self.call("other", "GET", "/api/v1/me/favorites").data
        self.check("offline favorite hides private media and description", offline_favorite_is_private(favorites, pid))
        self.expect("forged cross-surface logout cannot revoke admin", self.clients["anonymous"].request("POST", "/api/v1/auth/logout", cookie="qingye_session=" + admin_token))
        self.call("admin", "GET", "/api/v1/me")
        self.dashboard_security()
        self.purge_security()
        self.dashboard_contract(self.call("admin", "GET", "/api/v1/admin/dashboard?days=30&rank_by=consultations"), 30, "consultations")
        self.r22_security()
        self.r23_security()
        # Bounded password failures are deliberately last, so this phase does not
        # interfere with functional or concurrency evidence through shared limits.
        entry = self.credentials["other"]
        statuses = []
        for _ in range(14):
            response = self.clients["anonymous"].request("POST", "/api/v1/auth/login", {"method": "password", "phone": entry["phone"], "password": "always-wrong-acceptance-password"})
            statuses.append(response.status)
            if response.status == 429:
                break
        self.check("bounded login attempts reach Redis 429", 429 in statuses and all(code in (401, 429) for code in statuses), attempts=len(statuses), statuses=statuses)
        self.state["security_complete"] = True

    def run(self):
        try:
            if self.args.phase != "functional":
                self.check("functional fixtures exist", self.state.get("functional_complete") and bool(self.fixtures.get("product_id")))
            if self.args.phase == "security":
                self.check("concurrency phase already completed", self.state.get("concurrency_complete", False))
            for actor, surface in (("anonymous", "public"), ("admin", "admin")):
                health = self.call(actor, "GET", "/healthz", name=f"{surface} isolated API health")
                self.check(f"{surface} API surface is correct", health.json.get("surface") == surface)
            for actor in ("admin", "member", "other"):
                self.login(actor)
            getattr(self, self.args.phase)()
            if getattr(self.args, "response_crypto", False):
                from security_r24_acceptance import SecurityR24Checks
                # Reuse the same guarded targets, identities and fixture state.
                for name in ("r24_raw", "r24_plan"):
                    setattr(self, name, getattr(SecurityR24Checks, name).__get__(self))
                getattr(SecurityR24Checks, "r24_" + self.args.phase)(self)
            self.report["status"] = "passed"
        except CheckFailure as exc:
            self.report["status"] = "failed"
            self.report["failure"] = str(exc)
        except Exception as exc:
            # Exception messages may include request URLs or file paths; only
            # emit the exception class, never a traceback or credential-bearing URL.
            self.report["status"] = "error"
            self.report["failure"] = type(exc).__name__
        finally:
            self.report["finished_at"] = utcnow()
            self.report["run_id"] = self.state.get("run_id")
            self.report["fixtures"] = dict(self.fixtures)
            self.report["assertions"] = len(self.report["checks"])
            self.save()
            write_json(self.args.output, self.report)
        return self.report["status"] == "passed"


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--public-url", type=local_url, required=True)
    p.add_argument("--admin-url", type=local_url, required=True)
    p.add_argument("--public-origin", type=local_url, required=True)
    p.add_argument("--admin-origin", type=local_url, required=True)
    p.add_argument("--account-file", required=True)
    p.add_argument("--state", required=True, help="shared nonsecret fixture state across the three ordered phases")
    p.add_argument("--output", required=True)
    p.add_argument("--phase", choices=("functional", "concurrency", "security"), required=True)
    p.add_argument("--max-upload-bytes", type=int, default=10 * 1024 * 1024)
    p.add_argument("--response-crypto", action="store_true", help="require the R24 encrypted response protocol")
    p.add_argument("--no-oss-delivery", action="store_true", help="expect the encrypted deployment to have MEDIA_DELIVERY_ENABLED=false")
    return p


def main():
    args = parser().parse_args()
    # Explicitly block the existing business/demo ports even on loopback.
    if any(urllib.parse.urlsplit(url).port in (5178, 5179, 5180, 5181, 5182) for url in (args.public_url, args.admin_url)):
        raise SystemExit("Use dedicated acceptance API ports; existing business/demo ports are forbidden")
    try:
        suite = Suite(args)
        success = suite.run()
        print(json.dumps({"phase": args.phase, "status": suite.report["status"], "assertions": suite.report["assertions"], "report": str(Path(args.output).resolve())}, ensure_ascii=False))
        return 0 if success else 1
    except Exception as exc:
        print(json.dumps({"phase": args.phase, "status": "configuration_error", "error": str(exc) if isinstance(exc, CheckFailure) else type(exc).__name__}))
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
