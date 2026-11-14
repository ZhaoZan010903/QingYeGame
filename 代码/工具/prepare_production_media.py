#!/usr/bin/env python3
"""Verify existing legacy OSS originals for production takeover, without writes.

Only HeadObject and GetObjectAcl are used. Evidence is private and resumable;
at most four requests chains run concurrently (one by default). This does not
register replicas, switch permissions, modify a database, or claim a new copy.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, wait, FIRST_COMPLETED
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import sqlite3
import stat
import tempfile
import threading

SOURCE_PREFIX = 'qingye-test/media/'
EXPECTED_COUNT = 48332
EXPECTED_BYTES = 26841243424
SNAPSHOT_COUNT = 48345
SNAPSHOT_BYTES = 26845180398
REGION = 'cn-beijing'
INTERNAL_ENDPOINT = 'https://oss-cn-beijing-internal.aliyuncs.com'
MAX_FILE = 256 << 20
MAX_AGE = timedelta(hours=24)
SOURCE_FIELDS = ('media_id', 'purpose', 'source_storage_key', 'object_key', 'bucket',
                 'region', 'content_type', 'verified_source_sha256', 'remote_crc64',
                 'size_bytes', 'status', 'object_acl', 'verified_at',
                 'database_sha256', 'database_hash_available')
COPY_FIELDS = ('copy_source_if_match', 'target_etag', 'copied_crc64',
               'copy_response_etag', 'copy_request_id', 'shared_read_only')
FINAL_FILES = ('source-media-object-map-verified.jsonl', 'product-image-order.jsonl',
               'media-object-map-verified.jsonl', 'completion-receipt.json')
SOURCE_VERIFICATIONS = {'source_sha256', 'upload_content_md5', 'put_crc64',
                        'head_length_crc64_sha256_metadata', 'get_object_acl',
                        'source_preservation', 'product_image_order_mapping'}
VERIFICATIONS = ['source_manifest_sha256', 'source_head_size_crc64_sha256_metadata',
                 'get_object_acl', 'source_preservation', 'product_image_order_mapping']
local = threading.local()


class InvalidEvidence(ValueError):
    pass


def require(condition, code):
    if not condition:
        raise InvalidEvidence(code)


def now():
    return datetime.now(timezone.utc)


def timestamp(value):
    require(isinstance(value, str), 'invalid_timestamp')
    try:
        parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    except ValueError:
        raise InvalidEvidence('invalid_timestamp') from None
    require(parsed.tzinfo is not None, 'timestamp_missing_timezone')
    return parsed


def digest(data):
    return hashlib.sha256(data).hexdigest()


def decode(data):
    def unique(pairs):
        out, seen = {}, set()
        for key, value in pairs:
            require(key.lower() not in seen, 'duplicate_json_field')
            seen.add(key.lower())
            out[key] = value
        return out
    try:
        result = json.loads(data, object_pairs_hook=unique,
                            parse_constant=lambda _: (_ for _ in ()).throw(InvalidEvidence('invalid_json_number')))
    except (UnicodeError, json.JSONDecodeError):
        raise InvalidEvidence('invalid_json') from None
    require(isinstance(result, dict), 'json_object_required')
    return result


def regular(path, limit=MAX_FILE):
    path = Path(path).absolute()
    require(path.resolve() == path and not path.is_symlink(), 'symlink_not_allowed')
    info = path.stat()
    require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= limit, 'file_outside_bounds')
    with path.open('rb') as stream:
        data = stream.read(limit + 1)
    require(0 < len(data) <= limit, 'file_outside_bounds')
    return data


def records(raw):
    lines = raw.splitlines()
    require(0 < len(lines) <= 100000, 'manifest_outside_bounds')
    for line in lines:
        require(0 < len(line) <= 64 << 10, 'record_outside_bounds')
        yield decode(line)


def matches(pattern, value):
    return isinstance(value, str) and re.fullmatch(pattern, value) is not None


def valid_etag(value):
    return (isinstance(value, str) and 2 <= len(value) <= 256
            and all(0x21 <= ord(c) <= 0x7e for c in value)
            and ',' not in value and '*' not in value and not value.startswith('W/'))


def validate_record(row, bucket, completed):
    require(all(name in row for name in SOURCE_FIELDS if name != 'database_hash_available'), 'source_record_fields_missing')
    key = row['source_storage_key']
    require(matches(r'legacy/[0-9a-f]{64}', key) or
            matches(r'images/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|jpeg|png|webp)', key),
            'source_key_invalid')
    require(matches(r'[A-Za-z0-9_-]{1,100}', row['media_id']) and row['purpose'] == 'product'
            and row['object_key'] == SOURCE_PREFIX + key and row['bucket'] == bucket
            and row['region'] == REGION and row['object_acl'] == 'private'
            and row['status'] in ('copied_verified', 'existing_verified'), 'source_record_scope')
    require(type(row['size_bytes']) is int and 0 < row['size_bytes'] <= 64 << 20
            and row['content_type'] in ('image/png', 'image/jpeg', 'image/webp', 'image/gif')
            and matches(r'[0-9a-f]{64}', row['verified_source_sha256']), 'source_record_metadata')
    crc = row['remote_crc64']
    require(matches(r'0|[1-9][0-9]{0,19}', crc) and int(crc) <= (1 << 64) - 1, 'source_crc_invalid')
    # The original images/ records explicitly declare no database hash and
    # serialize its absent value as JSON null. Go decodes that string as "".
    # Restrict this normalization to that exact historical combination.
    if key.startswith('images/') and row.get('database_hash_available') is False and row['database_sha256'] is None:
        row = dict(row, database_sha256='')
    if 'database_hash_available' not in row:
        # Original legacy records predate this flag. Preserve Go's absent-bool
        # semantics while requiring their actual database digest to be present
        # and equal; never alter the raw source manifest or invent a digest.
        require(matches(r'legacy/[0-9a-f]{64}', key)
                and matches(r'[0-9a-f]{64}', row['database_sha256'])
                and row['database_sha256'] == row['verified_source_sha256'], 'source_database_hash_changed')
        row = dict(row, database_hash_available=False)
    require(type(row['database_hash_available']) is bool, 'source_database_hash_flag')
    require(isinstance(row['database_sha256'], str) and
            (not row['database_hash_available'] and row['database_sha256'] == ''
             or row['database_sha256'] == row['verified_source_sha256']), 'source_database_hash_changed')
    require(timestamp(row['verified_at']) <= completed, 'source_verification_time_invalid')
    return row


def load_inputs(bundle, order_path, expected_count, expected_bytes):
    bundle = Path(bundle).absolute()
    require(bundle.resolve() == bundle and bundle.is_dir(), 'source_bundle_path_invalid')
    names = ('media-object-map-verified.jsonl', 'final-record-hashes.json', 'completion-receipt.json', 'SUCCESS')
    raw = {name: regular(bundle / name, MAX_FILE if name.endswith('.jsonl') else 1 << 20) for name in names}
    hashes, receipt = decode(raw[names[1]]), decode(raw[names[2]])
    require(digest(raw[names[0]]) == hashes.get(names[0]) and digest(raw[names[2]]) == hashes.get(names[2]),
            'source_bundle_hash_changed')
    completed = timestamp(receipt.get('completed_at'))
    require(completed <= now() + timedelta(minutes=5)
            and raw['SUCCESS'].decode().strip() == receipt['completed_at'], 'source_completion_marker_changed')
    bucket = receipt.get('bucket')
    require(matches(r'[a-z0-9][a-z0-9-]{1,61}[a-z0-9]', bucket)
            and receipt.get('status') == 'snapshot_copy_complete'
            and receipt.get('prefix') == SOURCE_PREFIX and receipt.get('region') == REGION
            and receipt.get('verified_files') == SNAPSHOT_COUNT
            and receipt.get('verified_bytes') == SNAPSHOT_BYTES
            and receipt.get('source_stat_checks') == SNAPSHOT_COUNT
            and receipt.get('all_object_acls') == 'private'
            and receipt.get('source_files_preserved') is True
            and all(receipt.get(field) is False for field in
                    ('business_code_modified', 'business_database_modified', 'derived_thumbnail_cache_copied')),
            'source_snapshot_scope_invalid')
    require(isinstance(receipt.get('verification'), list)
            and SOURCE_VERIFICATIONS.issubset(receipt['verification']), 'source_snapshot_verification_incomplete')
    source, keys, total = {}, set(), 0
    for row in records(raw[names[0]]):
        row = validate_record(row, bucket, completed)
        require(row['media_id'] not in source and row['source_storage_key'] not in keys, 'duplicate_source_identity')
        source[row['media_id']] = row
        keys.add(row['source_storage_key'])
        total += row['size_bytes']
    require(len(source) == SNAPSHOT_COUNT and total == SNAPSHOT_BYTES, 'source_snapshot_totals_changed')
    order_raw = regular(order_path)
    selected, positions = {}, set()
    for association in records(order_raw):
        require(set(association) == {'product_id', 'media_id', 'sort_order'}, 'order_fields_invalid')
        media_id, product_id, order = (association[name] for name in ('media_id', 'product_id', 'sort_order'))
        require(matches(r'[A-Za-z0-9_-]{1,100}', product_id) and matches(r'[A-Za-z0-9_-]{1,100}', media_id)
                and type(order) is int and 0 <= order <= 100000, 'order_identity_invalid')
        require(media_id in source and media_id not in selected and (product_id, order) not in positions,
                'order_duplicate_or_unknown_identity')
        row = source[media_id]
        require(matches(r'legacy/[0-9a-f]{64}', row['source_storage_key']), 'order_nonlegacy_source')
        require(matches(r'[0-9a-f]{64}', row['database_sha256'])
                and row['database_sha256'] == row['verified_source_sha256'], 'selected_database_hash_missing_or_changed')
        selected[media_id] = {name: row[name] for name in SOURCE_FIELDS}
        positions.add((product_id, order))
    require(len(selected) == expected_count and sum(r['size_bytes'] for r in selected.values()) == expected_bytes,
            'selected_source_totals_changed')
    require({key for key, row in source.items() if row['source_storage_key'].startswith('legacy/')} == set(selected),
            'order_does_not_cover_exact_legacy_set')
    input_hashes = {str(bundle / name): digest(value) for name, value in raw.items()}
    input_hashes[str(Path(order_path).absolute())] = digest(order_raw)
    return raw[names[0]], order_raw, selected, bucket, input_hashes


def environment(path, bucket):
    values = {}
    for line in regular(path, 1 << 20).decode().splitlines():
        if line.strip() and not line.lstrip().startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            parsed = shlex.split(value)
            require(len(parsed) <= 1, 'environment_value_invalid')
            values[key] = parsed[0] if parsed else ''
    require(values.get('MEDIA_OSS_BUCKET') == bucket, 'configured_bucket_mismatch')
    require(values.get('MEDIA_OSS_ENDPOINT') in
            (INTERNAL_ENDPOINT, 'https://oss-cn-beijing.aliyuncs.com'), 'configured_endpoint_not_approved')
    require(values.get('MEDIA_OSS_ACCESS_KEY_ID') and values.get('MEDIA_OSS_ACCESS_KEY_SECRET'), 'credentials_missing')
    return values


def client(env):
    import alibabacloud_oss_v2 as oss
    if not hasattr(local, 'client'):
        local.client = oss.Client(oss.Config(region=REGION, endpoint=INTERNAL_ENDPOINT,
            credentials_provider=oss.credentials.StaticCredentialsProvider(
                env['MEDIA_OSS_ACCESS_KEY_ID'], env['MEDIA_OSS_ACCESS_KEY_SECRET']),
            retry_max_attempts=2, connect_timeout=10, readwrite_timeout=30))
    return local.client


def verify_one(row, env):
    import alibabacloud_oss_v2 as oss
    service = client(env)
    head = service.head_object(oss.HeadObjectRequest(bucket=row['bucket'], key=row['object_key']))
    require(head.content_length == row['size_bytes'] and str(head.hash_crc64) == row['remote_crc64'], 'head_size_crc_changed')
    require(head.metadata.get('source-sha256') == row['verified_source_sha256']
            and head.metadata.get('media-id') == row['media_id'] and head.content_type == row['content_type'],
            'head_metadata_changed')
    require(valid_etag(head.etag), 'head_etag_invalid')
    acl = service.get_object_acl(oss.GetObjectAclRequest(bucket=row['bucket'], key=row['object_key']))
    require(acl.acl == 'private', 'object_acl_not_private')
    verified_at = now().isoformat()
    require(timestamp(verified_at) >= timestamp(row['verified_at']), 'verification_clock_moved_backwards')
    return dict(row, status='existing_verified', source_object_key=row['object_key'],
                source_etag=head.etag, verified_at=verified_at)


def bounded_results(rows, env, workers):
    """No eager list of 48k futures; stop submitting after the first failure."""
    iterator = iter(rows)
    with ThreadPoolExecutor(max_workers=workers) as pool:
        pending, stopped = {}, False
        for _ in range(workers):
            row = next(iterator, None)
            if row is not None:
                pending[pool.submit(verify_one, row, env)] = row
        while pending:
            done, _ = wait(pending, return_when=FIRST_COMPLETED)
            for future in done:
                row = pending.pop(future)
                try:
                    result, error = future.result(), None
                except Exception as caught:
                    result, error, stopped = None, caught, True
                yield row, result, error
            if not stopped:
                while len(pending) < workers:
                    row = next(iterator, None)
                    if row is None:
                        break
                    pending[pool.submit(verify_one, row, env)] = row


def private_write(path, data):
    path = Path(path)
    require(not path.is_symlink() and (not path.exists() or path.is_file()), 'output_file_invalid')
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix='.evidence-', delete=False) as stream:
        temporary = Path(stream.name)
        try:
            os.chmod(temporary, 0o600)
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        except BaseException:
            temporary.unlink()
            raise
    os.replace(temporary, path)
    os.chmod(path, 0o600)


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode()


@contextmanager
def output_lock(work):
    lock = work / '.verification.lock'
    require(not lock.is_symlink(), 'output_lock_invalid')
    with lock.open('a+b') as stream:
        os.chmod(lock, 0o600)
        stream.seek(0)
        if os.name == 'nt':
            import msvcrt
            if not lock.stat().st_size:
                stream.write(b'0')
                stream.flush()
                stream.seek(0)
            msvcrt.locking(stream.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            yield
        finally:
            if os.name == 'nt':
                stream.seek(0)
                msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(stream, fcntl.LOCK_UN)


def safe_error(error):
    # Never serialize SDK exception strings: they can contain signed requests.
    return {'status': 'failed', 'reason': str(error) if isinstance(error, InvalidEvidence) else 'verification_failed'}


def validate_saved(row, source, started):
    expected = {name: source[name] for name in SOURCE_FIELDS if name not in ('status', 'verified_at')}
    require(all(row.get(name) == value for name, value in expected.items())
            and row.get('status') == 'existing_verified' and row.get('source_object_key') == source['object_key']
            and valid_etag(row.get('source_etag')) and all(name not in row for name in COPY_FIELDS), 'checkpoint_record_changed')
    verified = timestamp(row.get('verified_at'))
    require(started <= verified <= now() + timedelta(minutes=5) and verified >= timestamp(source['verified_at']),
            'checkpoint_time_invalid')


def check_inputs_unchanged(input_hashes):
    for path, checksum in input_hashes.items():
        require(digest(regular(path)) == checksum, 'input_changed_during_verification')


def run_locked(args, work, source_raw, order_raw, selected, bucket, input_hashes, env):
    binding = {'format': 'qingye-production-takeover-v1', 'bucket': bucket, 'prefix': SOURCE_PREFIX,
               'source_manifest_sha256': digest(source_raw), 'product_image_order_sha256': digest(order_raw),
               'expected_count': args.expected_count, 'expected_bytes': args.expected_bytes,
               'input_hashes': input_hashes}
    for name, raw in [('source-media-object-map-verified.jsonl', source_raw), ('product-image-order.jsonl', order_raw)]:
        path = work / name
        if path.exists():
            require(regular(path) == raw, 'bound_input_changed')
        else:
            private_write(path, raw)
    ledger_path = work / 'verification-ledger.sqlite'
    require(not ledger_path.is_symlink(), 'ledger_symlink_not_allowed')
    db = sqlite3.connect(ledger_path)
    os.chmod(ledger_path, 0o600)
    try:
        db.execute('CREATE TABLE IF NOT EXISTS metadata (id INTEGER PRIMARY KEY CHECK(id=1), binding TEXT NOT NULL, started_at TEXT NOT NULL)')
        db.execute('CREATE TABLE IF NOT EXISTS verified (media_id TEXT PRIMARY KEY, record TEXT NOT NULL)')
        meta = db.execute('SELECT binding,started_at FROM metadata WHERE id=1').fetchone()
        if meta:
            require(decode(meta[0]) == binding, 'checkpoint_scope_changed')
            started = timestamp(meta[1])
        else:
            require(db.execute('SELECT COUNT(*) FROM verified').fetchone()[0] == 0, 'checkpoint_binding_missing')
            started = now()
            db.execute('INSERT INTO metadata VALUES(1,?,?)', (json.dumps(binding, sort_keys=True), started.isoformat()))
            db.commit()
        require(timedelta(0) <= now() - started <= MAX_AGE, 'verification_session_expired_use_new_output')
        saved = set()
        for media_id, raw in db.execute('SELECT media_id,record FROM verified'):
            require(media_id in selected, 'checkpoint_unknown_identity')
            record = decode(raw)
            validate_saved(record, selected[media_id], started)
            saved.add(media_id)
        if (work / 'SUCCESS').exists():
            final_hashes = decode(regular(work / 'final-record-hashes.json', 1 << 20))
            require(set(final_hashes) == set(FINAL_FILES), 'completed_hash_scope_invalid')
            for name in FINAL_FILES:
                require(digest(regular(work / name)) == final_hashes[name], 'completed_bundle_changed')
            receipt = decode(regular(work / 'completion-receipt.json', 1 << 20))
            require(regular(work / 'SUCCESS', 4096).decode().strip() == receipt.get('completed_at')
                    and receipt.get('status') == 'production_takeover_verified'
                    and receipt.get('source_manifest_sha256') == binding['source_manifest_sha256']
                    and receipt.get('product_image_order_sha256') == binding['product_image_order_sha256']
                    and len(saved) == len(selected), 'completed_bundle_scope_invalid')
            check_inputs_unchanged(input_hashes)
            return {'mode': 'completed_bundle_unchanged', 'completed_at': receipt['completed_at'],
                    'files': len(saved), 'storage_mutations': 0, 'head_checks_this_run': 0}
        resumed, completed_now, failures = len(saved), 0, 0
        todo = (row for media_id, row in sorted(selected.items()) if media_id not in saved)
        for source, record, error in bounded_results(todo, env, args.workers):
            if error is not None:
                failures += 1
                private_write(work / 'last-error.json', json_bytes(safe_error(error)))
            else:
                validate_saved(record, source, started)
                db.execute('INSERT INTO verified VALUES(?,?)', (record['media_id'], json.dumps(record, sort_keys=True)))
                completed_now += 1
            if completed_now % 50 == 0 or error is not None:
                db.commit()
            if completed_now % 200 == 0 or error is not None:
                progress = {'verified': resumed + completed_now, 'resumed_verified': resumed,
                            'head_checks_this_run': completed_now, 'failed': failures, 'storage_mutations': 0}
                private_write(work / 'progress.json', json_bytes(progress))
                print(json.dumps(progress), flush=True)
        db.commit()
        require(failures == 0 and resumed + completed_now == len(selected), 'verification_incomplete_resume_same_output')
        require(now() - started <= MAX_AGE, 'verification_session_expired_use_new_output')
        check_inputs_unchanged(input_hashes)
        manifest = b''.join((raw + '\n').encode() for (raw,) in db.execute('SELECT record FROM verified ORDER BY media_id'))
        private_write(work / 'media-object-map-verified.jsonl', manifest)
        completed_at = now().isoformat()
        receipt = {'completed_at': completed_at, 'verification_started_at': started.isoformat(),
            'status': 'production_takeover_verified', 'reference_environment': 'production',
            'storage_mutations': 0, 'bucket': bucket, 'prefix': SOURCE_PREFIX, 'source_prefix': SOURCE_PREFIX,
            'region': REGION, 'verified_files': len(selected), 'verified_bytes': args.expected_bytes,
            'source_manifest_sha256': digest(source_raw), 'product_image_order_sha256': digest(order_raw),
            'all_object_acls': 'private', 'source_files_preserved': True, 'source_stat_checks': 0,
            'source_head_checks': len(selected), 'head_checks_this_run': completed_now, 'resumed_head_checks': resumed,
            'business_code_modified': False, 'business_database_modified': False,
            'derived_thumbnail_cache_copied': False, 'verification': VERIFICATIONS,
            'database_order_comparison': 'required_at_registration',
            'notes': 'HEAD/ACL evidence across one bounded verification session; no new upload/copy or full content download.'}
        private_write(work / 'completion-receipt.json', json_bytes(receipt))
        final_hashes = {name: digest(regular(work / name)) for name in FINAL_FILES}
        private_write(work / 'final-record-hashes.json', json_bytes(final_hashes))
        private_write(work / 'SUCCESS', (completed_at + '\n').encode())
        return {'mode': 'complete', 'files': len(selected), 'bytes': args.expected_bytes, 'storage_mutations': 0,
                'head_checks_this_run': completed_now, 'resumed_head_checks': resumed,
                'manifest_sha256': final_hashes['media-object-map-verified.jsonl']}
    finally:
        db.close()


def run(args):
    require(args.expected_count == EXPECTED_COUNT and args.expected_bytes == EXPECTED_BYTES, 'fixed_takeover_scope_changed')
    require(1 <= args.workers <= 4, 'workers_outside_bounds')
    os.umask(0o077)
    source_raw, order_raw, selected, bucket, input_hashes = load_inputs(
        args.source_bundle, args.product_order, args.expected_count, args.expected_bytes)
    env = environment(args.env_file, bucket)
    work, source_dir = Path(args.output).absolute(), Path(args.source_bundle).absolute()
    require(work.resolve() == work and work != source_dir and source_dir not in work.parents
            and work not in source_dir.parents, 'output_source_overlap')
    require(work not in Path(args.product_order).absolute().parents
            and work not in Path(args.env_file).absolute().parents, 'output_input_overlap')
    require(not any((parent / '.git').exists() for parent in [work, *work.parents]), 'output_must_be_outside_git')
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(work, 0o700)
    with output_lock(work):
        return run_locked(args, work, source_raw, order_raw, selected, bucket, input_hashes, env)


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-bundle', required=True)
    parser.add_argument('--product-order', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--env-file', required=True)
    parser.add_argument('--expected-count', type=int, default=EXPECTED_COUNT)
    parser.add_argument('--expected-bytes', type=int, default=EXPECTED_BYTES)
    parser.add_argument('--workers', type=int, choices=range(1, 5), default=1)
    return parser.parse_args()


if __name__ == '__main__':
    try:
        print(json.dumps(run(arguments())))
    except Exception as error:
        print(json.dumps(safe_error(error)))
        raise SystemExit(1)
