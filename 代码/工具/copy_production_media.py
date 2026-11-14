#!/usr/bin/env python3
"""Historical whole-copy tool; production copying was withdrawn for capacity.

The CLI rejects --apply before any copy. Retained preview and copy verification
code document the withdrawn operation; they do not authorize bypassing the guard.
Do not restore or rerun the whole-copy plan. Evidence and credentials stay private.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import sqlite3
import subprocess
import threading

SOURCE_PREFIX = 'qingye-test/media/'
TARGET_PREFIX = 'qingye-production/media/'
EXPECTED_COUNT = 48332
EXPECTED_BYTES = 26841243424
local = threading.local()


def now():
    return datetime.now(timezone.utc).isoformat()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def error_detail(error):
    # The v2 SDK wraps ServiceError in OperationError; its public unwrap method
    # is required to distinguish an exact existing-object conflict from outages.
    for _ in range(8):
        unwrap = getattr(error, 'unwrap', None)
        if not callable(unwrap):
            break
        nested = unwrap()
        if not isinstance(nested, Exception) or nested is error:
            break
        error = nested
    return error


def error_summary(error):
    detail = error_detail(error)
    code, status = getattr(detail, 'code', ''), getattr(detail, 'status_code', None)
    return {'type': type(error).__name__, 'cause_type': type(detail).__name__,
            'code': code if isinstance(code, str) and re.fullmatch(r'[A-Za-z0-9_-]{1,80}', code) else '',
            'http_status': status if isinstance(status, int) else None}


def environment(path):
    values = {}
    for line in Path(path).read_text().splitlines():
        if line.strip() and not line.lstrip().startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            parsed = shlex.split(value)
            values[key] = parsed[0] if parsed else ''
    return values


def selected_media(env):
    sql = """BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
COPY (SELECT row_to_json(x) FROM (
 SELECT m.id,m.storage_key,m.byte_size,m.sha256,m.mime_type,r.object_key,
 r.bucket,r.crc64,r.sha256 AS replica_sha256,r.manifest_sha256,
 p.id AS product_id,pi.sort_order
 FROM products p JOIN categories c ON c.id=p.category_id
 JOIN product_images pi ON pi.product_id=p.id JOIN media_files m ON m.id=pi.media_id
 JOIN media_legacy_replicas r ON r.media_id=m.id
 WHERE p.status='published' AND p.deleted_at IS NULL AND p.approved_version=p.version AND c.enabled
 AND m.status='ready' AND m.deleted_at IS NULL AND r.deleted_at IS NULL
 ORDER BY m.id) x) TO STDOUT;
COMMIT;"""
    result = subprocess.run(['runuser', '-u', 'postgres', '--', 'psql', '-d', 'qingye_test',
                            '-XqAt', '-v', 'ON_ERROR_STOP=1'], input=sql,
                            cwd='/tmp', text=True, capture_output=True, check=False)
    require(result.returncode == 0, 'source_database_query_failed')
    rows = [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]
    require(len(rows) == EXPECTED_COUNT, 'source_media_count_changed')
    require(sum(row['byte_size'] for row in rows) == EXPECTED_BYTES, 'source_media_bytes_changed')
    require(len({row['id'] for row in rows}) == EXPECTED_COUNT, 'duplicate_source_identity')
    return {row['id']: row for row in rows}


def order_manifest(selected):
    return b''.join((json.dumps({'media_id': key, 'product_id': selected[key]['product_id'],
                                'sort_order': selected[key]['sort_order']}, sort_keys=True) + '\n').encode()
                    for key in sorted(selected))


def validate_source(bundle, selected):
    raw = (bundle / 'media-object-map-verified.jsonl').read_bytes()
    source_manifest_sha = digest(raw)
    hashes = json.loads((bundle / 'final-record-hashes.json').read_text())
    require(source_manifest_sha == hashes['media-object-map-verified.jsonl'], 'source_manifest_hash_changed')
    receipt_raw = (bundle / 'completion-receipt.json').read_bytes()
    require(digest(receipt_raw) == hashes['completion-receipt.json'], 'source_receipt_hash_changed')
    receipt = json.loads(receipt_raw)
    require(receipt['status'] == 'snapshot_copy_complete' and receipt['prefix'] == SOURCE_PREFIX,
            'source_copy_incomplete')
    require((bundle / 'SUCCESS').read_text().strip() == receipt['completed_at'], 'source_marker_changed')
    rows = []
    for line in raw.splitlines():
        row = json.loads(line)
        live = selected.get(row['media_id'])
        if live is None:
            continue
        require(re.fullmatch(r'legacy/[0-9a-f]{64}', row['source_storage_key']), 'unsupported_source_key')
        require(row['source_storage_key'] == live['storage_key'], 'live_storage_key_changed')
        require(live['manifest_sha256'] == source_manifest_sha, 'registered_source_manifest_changed')
        require(row['purpose'] == 'product' and row['status'] in ('copied_verified', 'existing_verified'),
                'source_record_not_verified')
        require(row['object_key'] == SOURCE_PREFIX + row['source_storage_key'] == live['object_key'], 'source_key_changed')
        require(row['bucket'] == live['bucket'], 'source_bucket_changed')
        require(row['verified_source_sha256'] == live['sha256'] == live['replica_sha256'], 'source_sha_changed')
        require(str(row['remote_crc64']) == live['crc64'], 'source_crc_changed')
        require(row['size_bytes'] == live['byte_size'] and row['content_type'] == live['mime_type'], 'source_metadata_changed')
        require(row['object_acl'] == 'private', 'source_acl_invalid')
        rows.append(row)
    require(len(rows) == EXPECTED_COUNT and len({r['media_id'] for r in rows}) == EXPECTED_COUNT,
            'source_manifest_incomplete')
    return raw, rows


def client(env):
    import alibabacloud_oss_v2 as oss
    if not hasattr(local, 'client'):
        local.client = oss.Client(oss.Config(region='cn-beijing', endpoint=env['MEDIA_OSS_ENDPOINT'],
            credentials_provider=oss.credentials.StaticCredentialsProvider(
                env['MEDIA_OSS_ACCESS_KEY_ID'], env['MEDIA_OSS_ACCESS_KEY_SECRET']),
            retry_max_attempts=2, connect_timeout=10, readwrite_timeout=60))
    return local.client


def check_head(head, row):
    require(head.content_length == row['size_bytes'], 'head_size_changed')
    require(str(head.hash_crc64) == str(row['remote_crc64']), 'head_crc_changed')
    require(head.metadata.get('source-sha256') == row['verified_source_sha256'], 'head_sha_changed')
    require(head.metadata.get('media-id') == row['media_id'], 'head_identity_changed')
    require(head.content_type == row['content_type'], 'head_content_type_changed')


def copy_one(row, env, previous=None):
    import alibabacloud_oss_v2 as oss
    cl = client(env)
    source_key = row['object_key']
    target_key = TARGET_PREFIX + row['source_storage_key']
    source_head = cl.head_object(oss.HeadObjectRequest(bucket=row['bucket'], key=source_key))
    check_head(source_head, row)
    require(source_head.etag, 'source_etag_missing')
    status = 'copied_verified'
    copied_crc = ''
    response_etag, request_id = '', ''
    if previous is not None:
        for name in ('media_id', 'source_storage_key', 'bucket', 'size_bytes', 'content_type',
                     'verified_source_sha256', 'remote_crc64'):
            require(previous[name] == row[name], 'ledger_source_changed')
        require(previous['object_key'] == target_key and previous['source_object_key'] == source_key,
                'ledger_target_changed')
        require(previous['status'] in ('copied_verified', 'existing_verified'), 'ledger_not_verified')
        # A persisted success is never treated as proof that today's object is
        # still present/private. Recheck HEAD and ACL below on every resume.
        status = 'existing_verified'
    else:
        try:
            result = cl.copy_object(oss.CopyObjectRequest(bucket=row['bucket'], key=target_key,
                source_bucket=row['bucket'], source_key=source_key, if_match=source_head.etag,
                forbid_overwrite=True, metadata_directive='COPY', acl='private'))
            copied_crc = str(result.hash_crc64)
            response_etag, request_id = result.etag or '', getattr(result, 'request_id', '') or ''
            require(copied_crc == str(row['remote_crc64']), 'copy_crc_changed')
        except Exception as error:
            # Only an existing-object conflict is resumable. A timeout may have
            # copied bytes, but is retried by the next run with overwrite forbidden.
            detail = error_detail(error)
            if getattr(detail, 'status_code', None) != 409 or getattr(detail, 'code', '') != 'FileAlreadyExists':
                raise
            status = 'existing_verified'
    target_head = cl.head_object(oss.HeadObjectRequest(bucket=row['bucket'], key=target_key))
    check_head(target_head, row)
    require(target_head.etag, 'target_etag_missing')
    if status == 'copied_verified':
        require(response_etag == target_head.etag, 'copy_target_etag_changed')
    acl = cl.get_object_acl(oss.GetObjectAclRequest(bucket=row['bucket'], key=target_key))
    require(acl.acl == 'private', 'target_acl_not_private')
    return dict(row, object_key=target_key, object_acl='private', status=status,
                source_object_key=source_key, source_etag=source_head.etag,
                copy_source_if_match=source_head.etag if status == 'copied_verified' else '', target_etag=target_head.etag,
                copied_crc64=copied_crc, verified_at=now(),
                copy_response_etag=response_etag, copy_request_id=request_id,
                verification='conditional-server-copy+source-and-target-head+private-acl')


def run(args):
    # This one-off whole-copy plan was withdrawn by the owner: the bucket's
    # 40 GB capacity must not hold a second complete legacy gallery.
    # Keep preview/evidence utilities for diagnosis; CLI writes are retired.
    if args.apply:
        raise ValueError('whole_copy_disabled_by_40gb_capacity_constraint')
    os.umask(0o077)
    env = environment(args.environment_file)
    require('qingye_test' in env['DATABASE_URL'], 'source_database_scope')
    require(env['MEDIA_R20_ENVIRONMENT'] == 'test', 'source_environment_scope')
    require(env['MEDIA_OSS_ENDPOINT'] == 'https://oss-cn-beijing.aliyuncs.com', 'source_endpoint_scope')
    selected = selected_media(env)
    order = order_manifest(selected)
    raw_source, rows = validate_source(Path(args.source_bundle), selected)
    require(all(r['bucket'] == env['MEDIA_OSS_BUCKET'] for r in rows), 'configured_bucket_mismatch')
    summary = {'files': len(rows), 'bytes': sum(r['size_bytes'] for r in rows),
               'source_manifest_sha256': digest(raw_source), 'prefix': TARGET_PREFIX,
               'product_image_order_sha256': digest(order)}
    if not args.apply:
        print(json.dumps(dict(summary, mode='preview')))
        return
    work = Path(args.work_dir)
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    source_bundle = Path(args.source_bundle).resolve()
    require(not work.is_symlink() and work.resolve() != source_bundle and source_bundle not in work.resolve().parents
            and work.resolve() not in source_bundle.parents, 'source_work_directory_overlap')
    source_path = work / 'source-media-object-map-verified.jsonl'
    if source_path.exists():
        require(source_path.read_bytes() == raw_source, 'bound_source_changed')
    else:
        source_path.write_bytes(raw_source)
    ledger = sqlite3.connect(work / 'copy-ledger.sqlite')
    ledger.execute('CREATE TABLE IF NOT EXISTS copied (media_id TEXT PRIMARY KEY, record TEXT NOT NULL)')
    prior = {row[0]: json.loads(row[1]) for row in ledger.execute('SELECT media_id,record FROM copied')}
    require(set(prior).issubset(selected), 'ledger_source_set_changed')
    completed = set()
    failed = 0
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = {pool.submit(copy_one, row, env, prior.get(row['media_id'])): row['media_id'] for row in rows}
        for future in as_completed(futures):
            new_error = False
            try:
                row = future.result()
                ledger.execute('INSERT INTO copied(media_id,record) VALUES(?,?) ON CONFLICT(media_id) DO UPDATE SET record=excluded.record',
                               (row['media_id'], json.dumps(row, ensure_ascii=False)))
                ledger.commit()
                completed.add(row['media_id'])
            except Exception as error:
                failed += 1
                new_error = True
                # Never print SDK errors: they may contain signed requests or private object keys.
                (work / 'last-error.json').write_text(json.dumps(error_summary(error)))
            if (len(completed) + failed) % 200 == 0 or new_error:
                progress = dict(summary, completed=len(completed), failed=failed, updated_at=now())
                (work / 'progress.json').write_text(json.dumps(progress))
                print(json.dumps({'completed': len(completed), 'failed': failed}), flush=True)
            if failed >= 5:
                for item in futures:
                    item.cancel()
                break
    require(failed == 0 and len(completed) == len(rows), 'copy_incomplete')
    require(selected_media(env) == selected, 'source_database_changed_during_copy')
    (work / 'product-image-order.jsonl').write_bytes(order)
    records = [json.loads(row[0]) for row in ledger.execute('SELECT record FROM copied ORDER BY media_id')]
    require({r['media_id'] for r in records} == set(selected), 'target_identity_changed')
    manifest = b''.join((json.dumps(r, ensure_ascii=False) + '\n').encode() for r in records)
    (work / 'media-object-map-verified.jsonl').write_bytes(manifest)
    completed_at = now()
    receipt = dict(summary, completed_at=completed_at, status='server_side_copy_complete',
        bucket=env['MEDIA_OSS_BUCKET'], region='cn-beijing', verified_files=len(records),
        verified_bytes=summary['bytes'], source_prefix=SOURCE_PREFIX,
        all_object_acls='private', source_files_preserved=True, source_stat_checks=0,
        source_head_checks=len(records), business_code_modified=False, business_database_modified=False,
        derived_thumbnail_cache_copied=False, verification=['source_manifest_sha256',
        'source_head_size_crc64_sha256_metadata', 'copy_source_etag',
        'target_head_size_crc64_sha256_metadata', 'get_object_acl', 'source_preservation',
        'product_image_order_mapping'])
    receipt_raw = json.dumps(receipt, indent=2).encode()
    (work / 'completion-receipt.json').write_bytes(receipt_raw)
    (work / 'final-record-hashes.json').write_text(json.dumps({
        'source-media-object-map-verified.jsonl': digest(raw_source),
        'product-image-order.jsonl': digest(order),
        'media-object-map-verified.jsonl': digest(manifest),
        'completion-receipt.json': digest(receipt_raw)}, indent=2))
    (work / 'SUCCESS').write_text(completed_at + '\n')
    print(json.dumps(dict(summary, mode='complete', manifest_sha256=digest(manifest))))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-bundle', required=True)
    parser.add_argument('--work-dir', required=True)
    parser.add_argument('--environment-file', required=True)
    parser.add_argument('--workers', type=int, choices=range(1, 17), default=8)
    parser.add_argument('--apply', action='store_true')
    options = parser.parse_args()
    try:
        run(options)
    except Exception as error:
        print(json.dumps({'status': 'failed', 'type': type(error).__name__,
                          'reason': str(error) if isinstance(error, ValueError) else 'private_log_required'}))
        raise SystemExit(1)
