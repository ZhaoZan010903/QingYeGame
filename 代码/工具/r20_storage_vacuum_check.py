#!/usr/bin/env python3
"""Bounded SeaweedFS 4.53 checks. Default: read-only; never delete metadata.

Only the loopback qingye-acceptance S3 bucket/collection is allowed. --exercise
creates tiny PNG fixtures and deletes only their exact keys. --vacuum additionally
requires a confirmed, unique volume ID. No OSS access or prefix deletion exists.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import xml.etree.ElementTree as ET
import zlib


BUCKET = "qingye-acceptance"
MIB = 1024 * 1024
RESERVE = 2 * 1024 * MIB
FIXTURE_KEY = re.compile(r"^r20/acceptance/objects/vacuum[0-9a-f]{32}/probe[0-9]{2}/original$")
ACCEPTANCE_FILE = re.compile(r"^qingye-acceptance_[0-9]+\.(dat|idx)$")


class CheckFailure(Exception):
    pass


class ShellCheckFailure(CheckFailure):
    def __init__(self, message, output):
        super().__init__(message)
        self.output = output


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def loopback_origin(value):
    parsed = urllib.parse.urlsplit(value)
    try:
        valid = ipaddress.ip_address(parsed.hostname or "").is_loopback
        port = parsed.port
    except ValueError:
        valid, port = False, None
    if (not valid or not port or parsed.scheme != "http" or parsed.username
            or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment):
        raise CheckFailure("Only explicit HTTP loopback endpoints are allowed")
    return value.rstrip("/")


def checked_directory(value):
    path = Path(value).absolute()
    if not path.is_dir() or any(part.is_symlink() for part in (path, *path.parents)):
        raise CheckFailure("Volume directory must exist and have no symlink ancestors")
    return path


def shell(weed, master, commands, timeout=45):
    try:
        result = subprocess.run([weed, "shell", "-master=" + master],
                                input="\n".join(commands) + "\n", capture_output=True,
                                text=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired as error:
        partial = error.stdout or b""
        if isinstance(partial, bytes):
            partial = partial.decode("utf-8", errors="replace")
        raise ShellCheckFailure("Shell timed out; inspect server-side maintenance state before retrying", partial) from error
    output = result.stdout + result.stderr
    if result.returncode or re.search(r"(?im)(?:^|[> ]+)error:|failed to|permission denied", output):
        raise ShellCheckFailure("Seaweed shell failed; inspect the private shell log before retrying", output)
    return output


def parse_volumes(output):
    volumes = []
    for line in output.splitlines():
        match = re.search(r"\bvolume Id:(\d+), Size:(\d+),.*?Collection:([^,]*),", line)
        if match:
            deleted_count = re.search(r"\bDeleteCount:(\d+)", line)
            deleted_bytes = re.search(r"\bDeletedByteCount:(\d+)", line)
            volumes.append({"id": int(match[1]), "size": int(match[2]),
                            "collection": match[3], "remote": "RemoteStorageName:" in line,
                            "deleted_count": int(deleted_count[1]) if deleted_count else None,
                            "deleted_bytes": int(deleted_bytes[1]) if deleted_bytes else None})
    if not volumes:
        raise CheckFailure("Cannot parse 4.53 volume.list output; refusing to guess the target")
    names = {}
    for volume in volumes:
        names.setdefault(volume["id"], set()).add(volume["collection"])
    if any(len(values) != 1 for values in names.values()):
        raise CheckFailure("Duplicate volume IDs span collections; vacuum is unsafe")
    return volumes


def validate_volume(volumes, volume_id):
    matches = [v for v in volumes if v["id"] == volume_id]
    if not matches or any(v["collection"] != BUCKET or v["remote"] for v in matches):
        raise CheckFailure("Volume is absent, remote, or does not belong exclusively to qingye-acceptance")


def deletion_counters(volumes, volume_id):
    validate_volume(volumes, volume_id)
    matches = [v for v in volumes if v["id"] == volume_id]
    if any(v["deleted_count"] is None or v["deleted_bytes"] is None for v in matches):
        raise CheckFailure("Volume deletion counters unavailable; cannot verify propagation")
    # All replicas must expose progress; this deployment has a single local replica.
    return {"deleted_count": min(v["deleted_count"] for v in matches),
            "deleted_bytes": min(v["deleted_bytes"] for v in matches)}


def wait_for_delete_propagation(weed, master, volume_id, baseline, timeout=45):
    started = time.monotonic()
    samples = []
    while True:
        remaining = timeout - (time.monotonic() - started)
        if remaining <= 0:
            return {"observed": False, "baseline": baseline, "samples": samples}
        output = shell(weed, master, ["volume.list"], timeout=max(1, min(10, remaining)))
        current = deletion_counters(parse_volumes(output), volume_id)
        samples.append({"elapsed_seconds": round(time.monotonic() - started, 3), **current})
        if (current["deleted_count"] > baseline["deleted_count"]
                and current["deleted_bytes"] > baseline["deleted_bytes"]):
            return {"observed": True, "baseline": baseline, "samples": samples}
        remaining = timeout - (time.monotonic() - started)
        if remaining <= 0:
            return {"observed": False, "baseline": baseline, "samples": samples}
        time.sleep(min(3, remaining))


def local_files(directory):
    result = {}
    for path in directory.iterdir():
        if path.suffix not in (".dat", ".idx"):
            continue
        if path.is_symlink() or not path.is_file():
            raise CheckFailure("Unexpected volume-file type; refusing maintenance")
        stat = path.stat()
        result[path.name] = {"size": stat.st_size, "mtime_ns": stat.st_mtime_ns,
                             "allocated": getattr(stat, "st_blocks", 0) * 512}
    return result


def snapshot(directory):
    stat = os.statvfs(directory)
    return {"available_bytes": stat.f_bavail * stat.f_frsize, "files": local_files(directory)}


def vacuum_budget(directory, volume_id):
    data = directory / f"{BUCKET}_{volume_id}.dat"
    index = directory / f"{BUCKET}_{volume_id}.idx"
    if not data.is_file() or not index.is_file() or data.is_symlink() or index.is_symlink():
        raise CheckFailure("Expected local .dat/.idx pair missing; refusing to infer its location")
    # Deliberately conservative operational estimate; concurrent writes still need monitoring.
    workspace = max(512 * MIB, data.stat().st_size + index.stat().st_size * 4 + 256 * MIB)
    available = snapshot(directory)["available_bytes"]
    if available < RESERVE + workspace:
        raise CheckFailure("Insufficient free space for the 2 GiB reserve plus estimated vacuum workspace")
    return {"reserve_bytes": RESERVE, "estimated_workspace_bytes": workspace,
            "required_available_bytes": RESERVE + workspace, "available_bytes": available}


def png_bytes():
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    pixels = b"".join(b"\0" + os.urandom(32 * 3) for _ in range(32))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 32, 32, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(pixels)) + chunk(b"IEND", b""))


class S3:
    def __init__(self):
        if os.environ.get("MEDIA_R20_ENVIRONMENT") != "acceptance" or os.environ.get("MEDIA_STAGE_BUCKET") != BUCKET:
            raise CheckFailure("Explicit acceptance environment and qingye-acceptance bucket are required")
        self.origin = loopback_origin(os.environ.get("MEDIA_STAGE_ENDPOINT", ""))
        self.access = os.environ.get("MEDIA_STAGE_ACCESS_KEY_ID", "")
        self.secret = os.environ.get("MEDIA_STAGE_ACCESS_KEY_SECRET", "")
        if not self.access or not self.secret:
            raise CheckFailure("Missing staging credentials in the process environment")
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method, key="", payload=b"", query=None, expected=(200,)):
        if method not in ("GET", "HEAD", "PUT", "DELETE"):
            raise CheckFailure("Unsupported check operation")
        if method in ("PUT", "DELETE") and (not FIXTURE_KEY.fullmatch(key) or query):
            raise CheckFailure("Mutation requires an exact generated fixture key")
        if method == "PUT" and not (1024 <= len(payload) <= 10 * 1024):
            raise CheckFailure("Fixture size must remain between 1 and 10 KiB")
        path = "/" + BUCKET + ("/" + urllib.parse.quote(key, safe="/-_.~") if key else "")
        canonical_query = urllib.parse.urlencode(sorted((query or {}).items()), quote_via=urllib.parse.quote, safe="-_.~")
        now = dt.datetime.now(dt.timezone.utc)
        day, timestamp = now.strftime("%Y%m%d"), now.strftime("%Y%m%dT%H%M%SZ")
        digest = hashlib.sha256(payload).hexdigest()
        headers = {"host": urllib.parse.urlsplit(self.origin).netloc,
                   "x-amz-content-sha256": digest, "x-amz-date": timestamp}
        if method == "PUT":
            headers.update({"content-type": "image/png", "if-none-match": "*"})
        signed = ";".join(sorted(headers))
        canonical_headers = "".join(f"{name}:{headers[name]}\n" for name in sorted(headers))
        canonical = "\n".join((method, path, canonical_query, canonical_headers, signed, digest))
        scope = day + "/us-east-1/s3/aws4_request"
        to_sign = "AWS4-HMAC-SHA256\n" + timestamp + "\n" + scope + "\n" + hashlib.sha256(canonical.encode()).hexdigest()
        signing = ("AWS4" + self.secret).encode()
        for part in (day, "us-east-1", "s3", "aws4_request"):
            signing = hmac.new(signing, part.encode(), hashlib.sha256).digest()
        signature = hmac.new(signing, to_sign.encode(), hashlib.sha256).hexdigest()
        headers["Authorization"] = f"AWS4-HMAC-SHA256 Credential={self.access}/{scope}, SignedHeaders={signed}, Signature={signature}"
        request = urllib.request.Request(self.origin + path + ("?" + canonical_query if canonical_query else ""),
                                         data=payload if method == "PUT" else None, headers=headers, method=method)
        try:
            response = self.opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            status = response.code
            body = response.read(4 * MIB + 1)
        if status not in expected or len(body) > 4 * MIB:
            raise CheckFailure(f"S3 {method} returned unexpected HTTP {status}; response body omitted")
        return status, body

    def objects(self):
        result, token = {}, None
        for _ in range(100):
            query = {"list-type": "2", "max-keys": "1000"}
            if token:
                query["continuation-token"] = token
            _, raw = self.request("GET", query=query)
            root = ET.fromstring(raw)
            for item in root.findall("{*}Contents"):
                key = item.findtext("{*}Key")
                if not key:
                    raise CheckFailure("Empty key in bucket inventory")
                result[key] = {"size": item.findtext("{*}Size"), "etag": item.findtext("{*}ETag")}
            if root.findtext("{*}IsTruncated") != "true":
                return result
            next_token = root.findtext("{*}NextContinuationToken")
            if not next_token or next_token == token:
                raise CheckFailure("Invalid bucket-list continuation")
            token = next_token
        raise CheckFailure("Acceptance bucket inventory exceeds the bounded check limit")


def write_report(path, report):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + ".tmp")
    temp.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.chmod(temp, 0o600)
    os.replace(temp, path)


def run(args, report):
    if sys.platform != "linux":
        raise CheckFailure("Run this maintenance check on the Linux storage host")
    origin = loopback_origin("http://" + args.master)
    master = urllib.parse.urlsplit(origin).netloc
    directory = checked_directory(args.volume_dir)
    weed = shutil.which(args.weed)
    if not weed:
        raise CheckFailure("weed executable not found")
    version = subprocess.run([weed, "version"], text=True, capture_output=True, check=True, timeout=10)
    if not re.search(r"\b4\.53\b", version.stdout + version.stderr):
        raise CheckFailure("This check is pinned to SeaweedFS 4.53")
    report["version"] = (version.stdout + version.stderr).strip()
    report["collection"] = BUCKET
    report["topology_before"] = shell(weed, master, ["volume.list"])
    volumes = parse_volumes(report["topology_before"])
    report["before"] = snapshot(directory)
    if not args.exercise:
        report["status"] = "read_only_preflight_passed"
        return
    if args.vacuum:
        validate_volume(volumes, args.volume_id)
        report["budget"] = vacuum_budget(directory, args.volume_id)
    if report["before"]["available_bytes"] < RESERVE + 64 * MIB:
        raise CheckFailure("Less than 2 GiB reserve plus fixture safety allowance")
    client = S3()
    prior = client.objects()
    prefix = "r20/acceptance/objects/vacuum" + uuid.uuid4().hex + "/"
    fixtures = {prefix + f"probe{i:02d}/original": png_bytes() for i in range(14)}
    keep = list(fixtures)[:2]
    report["fixture_keys"] = list(fixtures)
    report["fixture_bytes"] = sum(map(len, fixtures.values()))
    report["prior_object_count"] = len(prior)
    created, deleted = set(), set()
    try:
        for key, payload in fixtures.items():
            client.request("HEAD", key, expected=(404,))
            client.request("PUT", key, payload, expected=(200, 201))
            created.add(key)
            _, received = client.request("GET", key)
            if hashlib.sha256(received).digest() != hashlib.sha256(payload).digest():
                raise CheckFailure("Fixture upload hash mismatch")
        report["after_upload"] = snapshot(directory)
        if args.vacuum:
            report["delete_baseline"] = deletion_counters(
                parse_volumes(shell(weed, master, ["volume.list"])), args.volume_id)
        for key in list(fixtures)[2:]:
            client.request("DELETE", key, expected=(200, 204))
            client.request("HEAD", key, expected=(404,))
            deleted.add(key)
        report["after_exact_delete"] = snapshot(directory)
        for key in keep:
            _, received = client.request("GET", key)
            if received != fixtures[key]:
                raise CheckFailure("Exact deletion changed a sibling canary")
        report["exact_key_delete_passed"] = True
        if args.vacuum:
            # S3 404 precedes Filer-to-volume deletion; wait for observable volume progress.
            report["delete_propagation"] = wait_for_delete_propagation(
                weed, master, args.volume_id, report["delete_baseline"])
            if not report["delete_propagation"]["observed"]:
                raise CheckFailure("Volume deletion did not propagate within 45 seconds; no vacuum requested")
            current = shell(weed, master, ["volume.list"])
            validate_volume(parse_volumes(current), args.volume_id)
            report["budget_before_vacuum"] = vacuum_budget(directory, args.volume_id)
            report["before_vacuum"] = snapshot(directory)
            started = time.monotonic()
            # Only this literal collection and validated integer can enter the mutation command.
            report["vacuum_output"] = shell(weed, master, ["lock",
                f"volume.vacuum -collection={BUCKET} -volumeId={args.volume_id} -garbageThreshold=0", "unlock"], timeout=240)
            report["vacuum_seconds"] = round(time.monotonic() - started, 3)
            report["after_vacuum"] = snapshot(directory)
            target = f"{BUCKET}_{args.volume_id}.dat"
            before_file = report["before_vacuum"]["files"][target]
            after_file = report["after_vacuum"]["files"].get(target)
            if after_file is None:
                raise CheckFailure("Vacuum removed the target volume unexpectedly")
            report["target_dat_reclaimed_bytes"] = before_file["size"] - after_file["size"]
            report["target_allocated_reclaimed_bytes"] = before_file["allocated"] - after_file["allocated"]
            report["physical_reclamation_observed"] = report["target_allocated_reclaimed_bytes"] > 0
        for key in keep:
            _, received = client.request("GET", key)
            if received != fixtures[key]:
                raise CheckFailure("Vacuum changed a surviving fixture")
        after = client.objects()
        if {key: value for key, value in after.items() if key not in fixtures} != prior:
            raise CheckFailure("Existing bucket inventory changed; concurrent activity or corruption needs review")
        report["prior_inventory_unchanged"] = True
        before_other = {k: v for k, v in report["before"]["files"].items() if not ACCEPTANCE_FILE.fullmatch(k)}
        after_other = {k: v for k, v in snapshot(directory)["files"].items() if not ACCEPTANCE_FILE.fullmatch(k)}
        report["other_collection_files_unchanged"] = before_other == after_other
        if before_other != after_other:
            raise CheckFailure("Other volume files changed during the check; isolation result is inconclusive")
        if args.vacuum and not report["physical_reclamation_observed"]:
            raise CheckFailure("No physical disk reclamation observed; the selected volume may hold no deleted fixtures")
        report["status"] = "exercise_passed" if not args.vacuum else "exercise_and_scoped_vacuum_passed"
    finally:
        # No listing drives deletion: only keys generated and successfully created by this invocation.
        remaining = []
        for key in sorted(created - deleted):
            try:
                client.request("DELETE", key, expected=(200, 204))
                client.request("HEAD", key, expected=(404,))
            except Exception:
                remaining.append(key)
        report["remaining_fixture_keys"] = remaining
        report["after_fixture_cleanup"] = snapshot(directory)
        if remaining:
            raise CheckFailure("Some generated fixtures remain; exact keys are recorded for retry")
    report["topology_after"] = shell(weed, master, ["volume.list"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--weed", default="weed")
    parser.add_argument("--master", default="127.0.0.1:9333")
    parser.add_argument("--volume-dir", required=True)
    parser.add_argument("--report", required=True)
    parser.add_argument("--exercise", action="store_true")
    parser.add_argument("--vacuum", action="store_true")
    parser.add_argument("--volume-id", type=int)
    args = parser.parse_args()
    if args.vacuum and (not args.exercise or not args.volume_id or args.volume_id < 1):
        parser.error("--vacuum requires --exercise and a confirmed positive --volume-id")
    report = {"started_at": dt.datetime.now(dt.timezone.utc).isoformat(), "status": "started"}
    try:
        run(args, report)
    except Exception as error:
        report["status"] = "failed"
        report["error"] = str(error) if isinstance(error, CheckFailure) else type(error).__name__
        if isinstance(error, ShellCheckFailure):
            report["shell_failure_output"] = error.output
        write_report(args.report, report)
        print("Check failed; inspect the private report. No global vacuum or metadata deletion was requested.", file=sys.stderr)
        return 1
    write_report(args.report, report)
    print(report["status"])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
