#!/usr/bin/env python3
"""Create one private acceptance PNG, inspect only its Filer metadata, then delete its exact S3 key."""
import argparse
import base64
import datetime as dt
import hashlib
import json
import re
import shutil
import sys
import urllib.parse
import urllib.request
import uuid

import r20_storage_vacuum_check as storage


def field(value, name, default=None):
    wanted = name.replace("_", "").lower()
    return next((data for key, data in value.items() if key.replace("_", "").lower() == wanted), default)


def inspect_metadata(entry, payload):
    content = field(entry, "content", "")
    inline = base64.b64decode(content, validate=True) if content else b""
    chunks = field(entry, "chunks", []) or []
    ids, file_ids = set(), []
    for chunk in chunks:
        fid = field(chunk, "file_id", "")
        if fid:
            if not re.fullmatch(r"[0-9]+,[0-9a-fA-F]+", fid):
                raise storage.CheckFailure("Unexpected chunk file ID format")
            ids.add(int(fid.split(",", 1)[0]))
            file_ids.append(fid)
        else:
            nested = field(chunk, "fid", {})
            volume_id = field(nested, "volume_id") if isinstance(nested, dict) else None
            if not volume_id:
                raise storage.CheckFailure("Cannot resolve chunk volume ID")
            ids.add(int(volume_id))
    if not inline and not chunks:
        raise storage.CheckFailure("Nonempty fixture has neither inline data nor chunks in metadata")
    if inline and inline != payload:
        raise storage.CheckFailure("Inline fixture content mismatch")
    return {"inline_bytes": len(inline), "inline_sha256": hashlib.sha256(inline).hexdigest() if inline else None,
            "chunk_count": len(chunks), "chunk_file_ids": file_ids, "volume_ids": sorted(ids)}


def run(args, report):
    client = storage.S3()
    filer = storage.loopback_origin(args.filer)
    master = urllib.parse.urlsplit(storage.loopback_origin("http://" + args.master)).netloc
    weed = shutil.which(args.weed)
    if not weed:
        raise storage.CheckFailure("weed executable not found")
    key = "r20/acceptance/objects/vacuum" + uuid.uuid4().hex + "/probe00/original"
    payload = storage.png_bytes()
    report.update({"key": key, "fixture_size": len(payload), "fixture_sha256": hashlib.sha256(payload).hexdigest()})
    created = False
    try:
        client.request("HEAD", key, expected=(404,))
        client.request("PUT", key, payload, expected=(200, 201))
        created = True
        _, received = client.request("GET", key)
        if received != payload:
            raise storage.CheckFailure("S3 fixture content mismatch")
        # Only the generated object's path is queried; no Filer writes or directory listing.
        url = filer + "/buckets/" + storage.BUCKET + "/" + urllib.parse.quote(key, safe="/-_.~") + "?metadata=true"
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), storage.NoRedirect())
        with opener.open(urllib.request.Request(url, headers={"Accept": "application/json"}), timeout=15) as response:
            raw = response.read(128 * 1024 + 1)
            if len(raw) > 128 * 1024:
                raise storage.CheckFailure("Unexpectedly large fixture metadata")
        entry = json.loads(raw)
        report["metadata"] = inspect_metadata(entry, payload)
        topology = storage.shell(weed, master, ["volume.list"])
        volumes = storage.parse_volumes(topology)
        report["topology"] = topology
        report["resolved_collections"] = {
            str(vid): sorted({v["collection"] for v in volumes if v["id"] == vid})
            for vid in report["metadata"]["volume_ids"]
        }
        report["isolated_collection_confirmed"] = bool(report["metadata"]["volume_ids"]) and all(
            names == [storage.BUCKET] for names in report["resolved_collections"].values())
        report["status"] = "metadata_observed"
    finally:
        if created:
            client.request("DELETE", key, expected=(200, 204))
            client.request("HEAD", key, expected=(404,))
            report["fixture_deleted"] = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--filer", required=True)
    parser.add_argument("--master", default="127.0.0.1:9333")
    parser.add_argument("--weed", default="weed")
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    report = {"started_at": dt.datetime.now(dt.timezone.utc).isoformat(), "status": "started", "fixture_deleted": False}
    try:
        run(args, report)
    except Exception as error:
        report["status"] = "failed"
        report["error"] = str(error) if isinstance(error, storage.CheckFailure) else type(error).__name__
        storage.write_report(args.report, report)
        print("Metadata probe failed; inspect the private report", file=sys.stderr)
        return 1
    storage.write_report(args.report, report)
    print(json.dumps({"status": report["status"], "metadata": report["metadata"],
                      "resolved_collections": report["resolved_collections"],
                      "isolated_collection_confirmed": report["isolated_collection_confirmed"],
                      "fixture_deleted": report["fixture_deleted"]}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
