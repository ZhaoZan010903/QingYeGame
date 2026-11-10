#!/usr/bin/env python3
"""Read-only reconciliation of source URLs, original files, manifest and test DB.

Run on the test host as root. No source URLs, credentials or product payloads are
written to the summary. --rehash independently reads every original's bytes.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess


def source_galleries(products):
    """Match import-legacy: skip deleted records, deduplicate within each gallery."""
    galleries = {}
    raw_count = 0
    for product in products:
        if product.get('is_del'):
            continue
        source_id = product.get('_id')
        if not isinstance(source_id, str) or not source_id or source_id in galleries:
            raise ValueError('Source product IDs must be present and unique')
        urls = [s.strip() for s in re.split(r',(?=\s*(?:https?:)?//)', product.get('content', '')) if s.strip()]
        raw_count += len(urls)
        urls = dict.fromkeys('https:' + url if url.startswith('//') else url for url in urls)
        galleries[source_id] = ['legacy/' + hashlib.sha256(url.encode()).hexdigest() for url in urls]
    return galleries, raw_count


def compare_galleries(expected, rows):
    """Compare every legacy product's complete ordered association, including gaps."""
    actual = {}
    issues = []

    def issue(source_id, error, **counts):
        # The summary contains only an opaque locator, never source IDs or URLs.
        issues.append({'product_ref': hashlib.sha256(str(source_id).encode()).hexdigest()[:16],
                       'error': error, **counts})

    for row in rows:
        source_id = row['legacy_id']
        if source_id in actual:
            issue(source_id, 'duplicate_database_product')
        actual[source_id] = row['images']
    matched = 0
    for source_id, keys in expected.items():
        if source_id not in actual:
            issue(source_id, 'missing_database_product')
            continue
        images = actual[source_id]
        ordered = [(item['sort_order'], item['storage_key']) for item in images]
        if ordered != list(enumerate(keys, start=1)):
            issue(source_id, 'gallery_association_or_order_mismatch',
                  expected_count=len(keys), database_count=len(images))
        else:
            matched += 1
    for source_id in set(actual) - set(expected):
        issue(source_id, 'unexpected_database_product')
    return matched, issues


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True)
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--media-root', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--rehash', action='store_true')
    args = parser.parse_args()
    root = Path(args.media_root).resolve()
    products = json.loads(Path(args.source).read_text(encoding='utf-8'))
    galleries, raw_association_count = source_galleries(products)
    association_count = sum(map(len, galleries.values()))
    expected = {key for gallery in galleries.values() for key in gallery}
    records = {}
    malformed_lines = 0
    for line in Path(args.manifest).read_text(encoding='utf-8').splitlines():
        try:
            item = json.loads(line)
            if item.get('state') == 'ready':
                records[item['key']] = item
        except (ValueError, KeyError):
            malformed_lines += 1
    # One SQL statement observes media and all product associations in one snapshot.
    sql = """SELECT json_build_object('media',(SELECT COALESCE(json_agg(t),'[]') FROM (
      SELECT m.storage_key,m.status,m.byte_size,m.sha256
      FROM media_files m WHERE m.storage_key LIKE 'legacy/%') t),
      'products',(SELECT COALESCE(json_agg(g),'[]') FROM (
        SELECT p.legacy_id,COALESCE((SELECT json_agg(json_build_object(
          'sort_order',pi.sort_order,'storage_key',m.storage_key) ORDER BY pi.sort_order,pi.media_id)
          FROM product_images pi JOIN media_files m ON m.id=pi.media_id
          WHERE pi.product_id=p.id),'[]') images
        FROM products p WHERE p.source='legacy') g))"""
    result = subprocess.run(['runuser', '-u', 'postgres', '--', 'psql', '-X', '-A', '-t',
                             '-v', 'ON_ERROR_STOP=1', '-d', 'qingye_test', '-c', sql],
                            cwd='/tmp', capture_output=True, text=True, check=True)
    database = json.loads(result.stdout)
    db_rows = database['media']
    db = {}
    for item in db_rows:
        db.setdefault(item['storage_key'], []).append(item)
    matched_galleries, issues = compare_galleries(galleries, database['products'])
    total_bytes = 0
    verified = 0
    for key in sorted(expected):
        entry, rows = records.get(key), db.get(key)
        path = root / key  # key is generated locally from a SHA256, never from the manifest.
        if not entry or not rows or not path.is_file():
            issues.append({'key': key, 'error': 'missing_source_manifest_db_or_file'})
            continue
        length = path.stat().st_size
        total_bytes += length
        if length != entry['bytes'] or any(row['status'] != 'ready' or length != row['byte_size']
                                         or entry['sha256'] != row['sha256'] for row in rows):
            issues.append({'key': key, 'error': 'status_size_or_hash_mismatch'})
            continue
        if args.rehash:
            digest = hashlib.sha256()
            with path.open('rb') as stream:
                while chunk := stream.read(1024 * 1024):
                    digest.update(chunk)
            if digest.hexdigest() != entry['sha256']:
                issues.append({'key': key, 'error': 'disk_hash_mismatch'})
                continue
        verified += 1
    extra_db = set(db) - expected
    summary = {
        'checked_at': datetime.now(timezone.utc).isoformat(),
        'source_products': len(galleries), 'source_image_associations': association_count,
        'source_raw_image_associations': raw_association_count,
        'database_legacy_products': len(database['products']),
        'verified_product_galleries': matched_galleries,
        'database_image_associations': sum(len(row['images']) for row in database['products']),
        'expected_originals': len(expected), 'manifest_ready': len(set(records) & expected),
        'database_originals': len(db_rows), 'verified_originals': verified,
        'total_original_bytes': total_bytes, 'total_original_gb': round(total_bytes / 10**9, 3),
        'total_original_gib': round(total_bytes / 1024**3, 3),
        'independent_disk_rehash': args.rehash, 'extra_database_keys': len(extra_db),
        # A shared source image may have one media row per product, by design.
        'shared_original_media_rows': len(db_rows) - len(db), 'malformed_manifest_lines': malformed_lines,
        'issue_count': len(issues), 'issues': issues[:20],
        'free_disk_bytes': shutil.disk_usage(root).free,
    }
    summary['passed'] = (verified == len(expected) and matched_galleries == len(galleries)
                         and not issues and not extra_db and not malformed_lines)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(summary, ensure_ascii=False))
    return 0 if summary['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
