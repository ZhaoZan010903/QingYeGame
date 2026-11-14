import contextlib
from datetime import timedelta
import importlib.util
import json
from pathlib import Path
import sqlite3
import sys
import tempfile
import threading
import types
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('prepare_production_media', Path(__file__).with_name('prepare_production_media.py'))
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)


class ReadOnlyOSS:
    """The test double has no object write/copy/delete method."""
    def __init__(self, rows):
        self.rows = {row['object_key']: row for row in rows}
        self.calls = []
        self.fail_acl = set()
        self.bad_head = {}

    def head_object(self, request):
        self.calls.append(('HEAD', request.bucket, request.key))
        row = self.rows[request.key]
        value = types.SimpleNamespace(content_length=row['size_bytes'], hash_crc64=row['remote_crc64'],
            metadata={'source-sha256': row['verified_source_sha256'], 'media-id': row['media_id']},
            content_type=row['content_type'], etag='"exact-etag"')
        for name, item in self.bad_head.items():
            setattr(value, name, item)
        return value

    def get_object_acl(self, request):
        self.calls.append(('ACL', request.bucket, request.key))
        return types.SimpleNamespace(acl='public-read' if request.key in self.fail_acl else 'private')


class ProductionMediaTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bundle = self.root / 'source'
        self.bundle.mkdir()
        self.output = self.root / 'takeover'
        self.order_path = self.root / 'order.jsonl'
        self.env_path = self.root / 'private.env'
        self.completed = (tool.now() - timedelta(hours=2)).isoformat()
        self.rows = [self.row(i) for i in range(3)]
        self.rows[2]['source_storage_key'] = 'images/00000000-0000-0000-0000-000000000000.png'
        self.rows[2]['object_key'] = tool.SOURCE_PREFIX + self.rows[2]['source_storage_key']
        self.rows[2]['database_sha256'] = None
        self.rows[2]['database_hash_available'] = False
        self.write_bundle()
        self.order_path.write_bytes(b''.join((json.dumps({'product_id': 'product', 'media_id': f'm{i}', 'sort_order': i}) + '\n').encode() for i in range(2)))
        self.env_path.write_text('MEDIA_OSS_BUCKET=fixture-bucket\nMEDIA_OSS_ENDPOINT=https://oss-cn-beijing.aliyuncs.com\nMEDIA_OSS_ACCESS_KEY_ID=private-id\nMEDIA_OSS_ACCESS_KEY_SECRET=never-print-this\n')
        self.args = types.SimpleNamespace(source_bundle=str(self.bundle), product_order=str(self.order_path),
            output=str(self.output), env_file=str(self.env_path), expected_count=2, expected_bytes=201, workers=1)
        self.service = ReadOnlyOSS(self.rows)
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.stack.enter_context(patch.multiple(tool, EXPECTED_COUNT=2, EXPECTED_BYTES=201, SNAPSHOT_COUNT=3, SNAPSHOT_BYTES=303))
        self.stack.enter_context(patch.dict(sys.modules, alibabacloud_oss_v2=types.SimpleNamespace(
            HeadObjectRequest=types.SimpleNamespace, GetObjectAclRequest=types.SimpleNamespace)))
        self.stack.enter_context(patch.object(tool, 'client', return_value=self.service))

    def row(self, i):
        return {'media_id': f'm{i}', 'purpose': 'product', 'source_storage_key': f'legacy/{i:064x}',
                'object_key': tool.SOURCE_PREFIX + f'legacy/{i:064x}', 'bucket': 'fixture-bucket', 'region': tool.REGION,
                'content_type': 'image/png', 'verified_source_sha256': 'a' * 64, 'remote_crc64': str(i + 42),
                'size_bytes': i + 100, 'status': 'copied_verified', 'object_acl': 'private', 'verified_at': self.completed,
                'database_sha256': 'a' * 64, 'database_hash_available': True}

    def write_bundle(self, changes=None):
        manifest = b''.join((json.dumps(row) + '\n').encode() for row in self.rows)
        receipt = {'completed_at': self.completed, 'status': 'snapshot_copy_complete', 'prefix': tool.SOURCE_PREFIX,
            'bucket': 'fixture-bucket', 'region': tool.REGION, 'verified_files': 3, 'verified_bytes': 303,
            'source_stat_checks': 3, 'all_object_acls': 'private', 'source_files_preserved': True,
            'business_code_modified': False, 'business_database_modified': False, 'derived_thumbnail_cache_copied': False,
            'verification': sorted(tool.SOURCE_VERIFICATIONS)}
        receipt.update(changes or {})
        receipt_raw = tool.json_bytes(receipt)
        (self.bundle / 'media-object-map-verified.jsonl').write_bytes(manifest)
        (self.bundle / 'completion-receipt.json').write_bytes(receipt_raw)
        (self.bundle / 'final-record-hashes.json').write_bytes(tool.json_bytes({
            'media-object-map-verified.jsonl': tool.digest(manifest), 'completion-receipt.json': tool.digest(receipt_raw)}))
        (self.bundle / 'SUCCESS').write_text(self.completed + '\n')

    def run_quietly(self):
        with contextlib.redirect_stdout(__import__('io').StringIO()):
            return tool.run(self.args)

    def test_complete_evidence_is_read_only_original_path_and_no_copy_claim(self):
        result = self.run_quietly()
        self.assertEqual(result['mode'], 'complete')
        self.assertEqual(len(self.service.calls), 4)
        self.assertEqual({item[0] for item in self.service.calls}, {'HEAD', 'ACL'})
        self.assertTrue(all(item[2].startswith(tool.SOURCE_PREFIX + 'legacy/') for item in self.service.calls))
        hashes = json.loads((self.output / 'final-record-hashes.json').read_bytes())
        self.assertEqual(set(hashes), set(tool.FINAL_FILES))
        for name, checksum in hashes.items():
            self.assertEqual(tool.digest((self.output / name).read_bytes()), checksum)
        self.assertEqual((self.output / 'source-media-object-map-verified.jsonl').read_bytes(),
                         (self.bundle / 'media-object-map-verified.jsonl').read_bytes())
        receipt = json.loads((self.output / 'completion-receipt.json').read_bytes())
        self.assertEqual(receipt['status'], 'production_takeover_verified')
        self.assertEqual(receipt['reference_environment'], 'production')
        self.assertEqual(receipt['storage_mutations'], 0)
        self.assertEqual(receipt['source_head_checks'], 2)
        self.assertEqual(receipt['source_stat_checks'], 0)
        self.assertEqual(receipt['prefix'], tool.SOURCE_PREFIX)
        self.assertEqual(receipt['verification'], tool.VERIFICATIONS)
        self.assertEqual((self.output / 'SUCCESS').read_text().strip(), receipt['completed_at'])
        for row in tool.records((self.output / 'media-object-map-verified.jsonl').read_bytes()):
            self.assertEqual(row['source_object_key'], row['object_key'])
            self.assertEqual(row['status'], 'existing_verified')
            self.assertEqual(row['source_etag'], '"exact-etag"')
            self.assertFalse(set(row) & set(tool.COPY_FIELDS))

    def test_resume_reuses_bound_success_and_rechecks_failed_object(self):
        self.service.fail_acl.add(self.rows[1]['object_key'])
        with self.assertRaisesRegex(tool.InvalidEvidence, 'verification_incomplete'):
            self.run_quietly()
        self.assertFalse((self.output / 'SUCCESS').exists())
        first_calls = len(self.service.calls)
        self.service.fail_acl.clear()
        result = self.run_quietly()
        self.assertEqual(result['resumed_head_checks'], 1)
        self.assertEqual(result['head_checks_this_run'], 1)
        self.assertEqual(len(self.service.calls) - first_calls, 2)
        self.assertEqual(self.service.calls[-1][2], self.rows[1]['object_key'])

    def test_completed_bundle_is_idempotent_without_claiming_new_heads(self):
        self.run_quietly()
        previous = (self.output / 'completion-receipt.json').read_bytes()
        call_count = len(self.service.calls)
        result = self.run_quietly()
        self.assertEqual(result['mode'], 'completed_bundle_unchanged')
        self.assertEqual(result['head_checks_this_run'], 0)
        self.assertEqual(len(self.service.calls), call_count)
        self.assertEqual((self.output / 'completion-receipt.json').read_bytes(), previous)

    def test_resume_rejects_changed_order_before_new_network_requests(self):
        self.service.fail_acl.add(self.rows[1]['object_key'])
        with self.assertRaises(tool.InvalidEvidence): self.run_quietly()
        self.order_path.write_text(self.order_path.read_text().replace('"sort_order": 1', '"sort_order": 2'))
        count = len(self.service.calls)
        with self.assertRaisesRegex(tool.InvalidEvidence, 'bound_input_changed'):
            self.run_quietly()
        self.assertEqual(len(self.service.calls), count)

    def test_source_bundle_requires_original_completed_snapshot(self):
        self.write_bundle({'status': 'production_takeover_verified'})
        with self.assertRaisesRegex(tool.InvalidEvidence, 'source_snapshot_scope_invalid'):
            self.run_quietly()
        self.assertEqual(self.service.calls, [])

    def test_source_hash_and_completion_marker_are_not_optional(self):
        (self.bundle / 'SUCCESS').write_text('changed')
        with self.assertRaisesRegex(tool.InvalidEvidence, 'source_completion_marker_changed'):
            self.run_quietly()
        self.write_bundle()
        with (self.bundle / 'media-object-map-verified.jsonl').open('ab') as stream: stream.write(b'\n')
        with self.assertRaisesRegex(tool.InvalidEvidence, 'source_bundle_hash_changed'):
            self.run_quietly()

    def test_order_rejects_duplicates_unknown_nonlegacy_and_malformed_fields(self):
        cases = [
            [{'product_id': 'product', 'media_id': 'm0', 'sort_order': 0}] * 2,
            [{'product_id': 'product', 'media_id': 'unknown', 'sort_order': 0}],
            [{'product_id': 'product', 'media_id': 'm2', 'sort_order': 0}],
            [{'product_id': 'product', 'media_id': 'm0', 'sort_order': True}],
        ]
        for values in cases:
            with self.subTest(values=values):
                self.order_path.write_bytes(b''.join(tool.json_bytes(x).replace(b'\n', b'') + b'\n' for x in values))
                with self.assertRaises(tool.InvalidEvidence): self.run_quietly()
        self.assertEqual(self.service.calls, [])

    def test_mismatching_metadata_etag_or_acl_prevents_success(self):
        changes = [{'content_length': 0}, {'hash_crc64': '999'}, {'content_type': 'text/plain'},
                   {'metadata': {'source-sha256': 'b' * 64, 'media-id': 'm0'}}, {'etag': '*'}]
        for values in changes:
            with self.subTest(values=values):
                self.service.bad_head = values
                with self.assertRaises(tool.InvalidEvidence): tool.verify_one(self.rows[0], {})
        self.service.bad_head = {}
        self.service.fail_acl.add(self.rows[0]['object_key'])
        with self.assertRaisesRegex(tool.InvalidEvidence, 'object_acl_not_private'):
            tool.verify_one(self.rows[0], {})

    def test_fixed_scope_worker_cap_and_unapproved_endpoint_fail_before_network(self):
        self.args.workers = 5
        with self.assertRaisesRegex(tool.InvalidEvidence, 'workers_outside_bounds'): self.run_quietly()
        self.args.workers = 1
        self.args.expected_count = 1
        with self.assertRaisesRegex(tool.InvalidEvidence, 'fixed_takeover_scope_changed'): self.run_quietly()
        self.args.expected_count = 2
        self.env_path.write_text(self.env_path.read_text().replace('https://oss-cn-beijing.aliyuncs.com', 'https://example.com'))
        with self.assertRaisesRegex(tool.InvalidEvidence, 'configured_endpoint_not_approved'): self.run_quietly()
        self.assertEqual(self.service.calls, [])

    def test_input_change_during_scan_prevents_completion_marker(self):
        original = tool.verify_one
        def mutate(row, env):
            result = original(row, env)
            if row['media_id'] == 'm1': self.order_path.write_bytes(self.order_path.read_bytes() + b'\n')
            return result
        with patch.object(tool, 'verify_one', side_effect=mutate):
            with self.assertRaisesRegex(tool.InvalidEvidence, 'input_changed_during_verification'):
                self.run_quietly()
        self.assertFalse((self.output / 'SUCCESS').exists())

    def test_resumed_session_cannot_reuse_stale_heads(self):
        self.run_quietly()
        with contextlib.closing(sqlite3.connect(self.output / 'verification-ledger.sqlite')) as db:
            db.execute('UPDATE metadata SET started_at=?', ((tool.now() - timedelta(days=2)).isoformat(),))
            db.commit()
        with self.assertRaisesRegex(tool.InvalidEvidence, 'verification_session_expired'):
            self.run_quietly()

    def test_future_queue_is_bounded_and_stops_scheduling_on_error(self):
        produced, gate, entered = [], threading.Event(), threading.Event()
        def source():
            for i in range(1000):
                produced.append(i)
                yield {'media_id': str(i)}
        def blocked(row, env):
            entered.set()
            gate.wait(2)
            raise RuntimeError('private signed URL must not escape')
        result = []
        with patch.object(tool, 'verify_one', side_effect=blocked):
            thread = threading.Thread(target=lambda: result.extend(tool.bounded_results(source(), {}, 2)))
            thread.start()
            self.assertTrue(entered.wait(1))
            self.assertLessEqual(len(produced), 2)
            gate.set()
            thread.join(3)
            self.assertFalse(thread.is_alive())
        self.assertEqual(len(produced), 2)
        self.assertEqual(len(result), 2)

    def test_output_cannot_overlap_source_or_repository_and_errors_are_redacted(self):
        self.args.output = str(self.bundle / 'nested')
        with self.assertRaisesRegex(tool.InvalidEvidence, 'output_source_overlap'): self.run_quietly()
        self.args.output = str(self.output)
        self.output.mkdir()
        inside_order = self.output / 'progress.json'
        inside_order.write_bytes(self.order_path.read_bytes())
        self.args.product_order = str(inside_order)
        with self.assertRaisesRegex(tool.InvalidEvidence, 'output_input_overlap'): self.run_quietly()
        self.args.product_order = str(self.order_path)
        self.args.output = str(self.root / 'repo' / 'private')
        (self.root / 'repo' / '.git').mkdir(parents=True)
        with self.assertRaisesRegex(tool.InvalidEvidence, 'output_must_be_outside_git'): self.run_quietly()
        self.assertNotIn('never-print-this', json.dumps(tool.safe_error(RuntimeError('never-print-this'))))

    def test_duplicate_json_fields_and_foreign_database_digest_are_rejected(self):
        with self.assertRaisesRegex(tool.InvalidEvidence, 'duplicate_json_field'):
            tool.decode('{"media_id":"m0","MEDIA_ID":"m1"}')
        self.rows[0]['database_sha256'] = 'b' * 64
        self.write_bundle()
        with self.assertRaisesRegex(tool.InvalidEvidence, 'source_database_hash_changed'): self.run_quietly()

    def test_original_legacy_without_database_flag_preserves_source_and_normalizes_false(self):
        # Matches the observed original bundle: legacy rows omit only this flag;
        # images rows explicitly use false + null. No private production values
        # are used, and the raw null remains intact in the complete source copy.
        for row in self.rows[:2]:
            del row['database_hash_available']
        self.write_bundle()
        original = (self.bundle / 'media-object-map-verified.jsonl').read_bytes()
        result = self.run_quietly()
        self.assertEqual(result['mode'], 'complete')
        self.assertEqual((self.bundle / 'media-object-map-verified.jsonl').read_bytes(), original)
        self.assertEqual((self.output / 'source-media-object-map-verified.jsonl').read_bytes(), original)
        original_rows = list(tool.records(original))
        self.assertIsNone(original_rows[2]['database_sha256'])
        self.assertIs(original_rows[2]['database_hash_available'], False)
        receipt = json.loads((self.output / 'completion-receipt.json').read_bytes())
        self.assertEqual(receipt['source_manifest_sha256'], tool.digest(original))
        for row in tool.records((self.output / 'media-object-map-verified.jsonl').read_bytes()):
            self.assertIs(row['database_hash_available'], False)
            self.assertEqual(row['database_sha256'], 'a' * 64)
            self.assertEqual(row['database_sha256'], row['verified_source_sha256'])

    def test_missing_database_flag_never_invents_or_relaxes_digest(self):
        for invalid_digest in ['', None, 'not-a-hash', 'b' * 64]:
            with self.subTest(digest=invalid_digest):
                self.rows[0].pop('database_hash_available', None)
                self.rows[0]['database_sha256'] = invalid_digest
                self.write_bundle()
                with self.assertRaisesRegex(tool.InvalidEvidence, 'source_database_hash_changed'):
                    self.run_quietly()
        self.assertEqual(self.service.calls, [])

    def test_null_database_hash_is_only_normalized_for_explicit_false_images(self):
        image = dict(self.rows[2])
        normalized = tool.validate_record(image, image['bucket'], tool.timestamp(self.completed))
        self.assertEqual(normalized['database_sha256'], '')
        self.assertIsNone(image['database_sha256'])
        for row in [dict(image, database_hash_available=True),
                    {k: v for k, v in image.items() if k != 'database_hash_available'},
                    dict(self.rows[0], database_sha256=None, database_hash_available=False)]:
            with self.subTest(key=row['source_storage_key']):
                with self.assertRaises(tool.InvalidEvidence):
                    tool.validate_record(row, row['bucket'], tool.timestamp(self.completed))

    def test_other_nullable_source_fields_remain_invalid_like_go_validation(self):
        for field in tool.SOURCE_FIELDS:
            if field == 'database_sha256':
                continue
            with self.subTest(field=field):
                row = dict(self.rows[0], **{field: None})
                with self.assertRaises(tool.InvalidEvidence):
                    tool.validate_record(row, 'fixture-bucket', tool.timestamp(self.completed))

    def test_selected_legacy_requires_database_digest_and_other_fields_remain_required(self):
        self.rows[0]['database_hash_available'] = False
        self.rows[0]['database_sha256'] = ''
        self.write_bundle()
        with self.assertRaisesRegex(tool.InvalidEvidence, 'selected_database_hash_missing_or_changed'):
            self.run_quietly()
        self.rows[0]['database_sha256'] = 'a' * 64
        del self.rows[0]['content_type']
        self.write_bundle()
        with self.assertRaisesRegex(tool.InvalidEvidence, 'source_record_fields_missing'):
            self.run_quietly()
        self.assertEqual(self.service.calls, [])


if __name__ == '__main__':
    unittest.main()
