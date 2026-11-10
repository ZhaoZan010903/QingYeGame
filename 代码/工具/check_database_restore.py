#!/usr/bin/env python3
"""Back up only QingYe's test DB, restore into a fresh temporary DB and compare.

Run as root on the test server. The temporary database is created by this run;
an existing database is never overwritten or dropped.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--output', required=True, help='Private backup directory')
args = parser.parse_args()
directory = Path(args.output)
directory.mkdir(parents=True, exist_ok=True, mode=0o700)
stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')
backup = directory / (stamp + '-qingye-test.dump')
database = 'qingye_restore_' + uuid.uuid4().hex[:12]

def run(command, **kwargs):
    return subprocess.run(['runuser', '-u', 'postgres', '--'] + command,
                          check=True, cwd='/tmp', **kwargs)

sql = """SELECT json_build_object(
 'products',(SELECT count(*) FROM products),
 'legacy_products',(SELECT count(*) FROM products WHERE source='legacy'),
 'image_links',(SELECT count(*) FROM product_images),
 'media',(SELECT count(*) FROM media_files),
 'users',(SELECT count(*) FROM users),
 'contacts',(SELECT count(*) FROM contacts),
 'reviews',(SELECT count(*) FROM product_reviews),
 'legacy_catalog_checksum',(SELECT md5(string_agg(id||'|'||title||'|'||COALESCE(legacy_price,'')||'|'||price_unit,'\n' ORDER BY id)) FROM products WHERE source='legacy')
);"""
def counts(name):
    return json.loads(run(['psql', '-XAt', '-v', 'ON_ERROR_STOP=1', '-d', name, '-c', sql],
                          capture_output=True, text=True).stdout)

before = counts('qingye_test')
with backup.open('xb') as destination:
    run(['pg_dump', '-Fc', '--no-password', 'qingye_test'], stdout=destination)
backup.chmod(0o600)
created = False
try:
    run(['createdb', database])
    created = True
    with backup.open('rb') as source:
        run(['pg_restore', '--exit-on-error', '--no-password', '-d', database],
            stdin=source, capture_output=True)
    restored = counts(database)
    if restored != before:
        raise RuntimeError('Restore counts differ; check for concurrent source writes and re-run')
finally:
    if created:
        run(['dropdb', database])
report = {'checked_at': stamp, 'restored': True, 'counts': restored,
          'backup_bytes': backup.stat().st_size,
          'backup_sha256': hashlib.sha256(backup.read_bytes()).hexdigest(),
          'scope': 'Database only; media bytes are verified separately, this is not an off-host backup'}
(directory/(stamp + '-restore-check.json')).write_text(json.dumps(report, ensure_ascii=False, indent=2))
print(json.dumps(report, ensure_ascii=False))
