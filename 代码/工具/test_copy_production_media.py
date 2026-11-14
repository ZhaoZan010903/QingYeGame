import contextlib
import importlib.util
from pathlib import Path
import sys
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('copy_production_media', Path(__file__).with_name('copy_production_media.py'))
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)


class ServiceFailure(Exception):
    def __init__(self, status, code):
        self.status_code, self.code = status, code


class WrappedFailure(Exception):
    def __init__(self, error):
        self.error = error

    def unwrap(self):
        return self.error


class CopyClient:
    def __init__(self, row, conflict=False):
        self.row, self.conflict = row, conflict
        self.copies, self.heads, self.acls = [], [], []
        self.acl = 'private'
        self.target_sha = row['verified_source_sha256']

    def head_object(self, request):
        self.heads.append(request)
        sha = self.target_sha if request.key.startswith(tool.TARGET_PREFIX) else self.row['verified_source_sha256']
        return types.SimpleNamespace(content_length=self.row['size_bytes'], hash_crc64=self.row['remote_crc64'],
            metadata={'source-sha256': sha, 'media-id': self.row['media_id']},
            content_type=self.row['content_type'], etag='"exact-source-etag"')

    def copy_object(self, request):
        self.copies.append(request)
        if self.conflict:
            raise WrappedFailure(ServiceFailure(409, 'FileAlreadyExists'))
        return types.SimpleNamespace(hash_crc64=self.row['remote_crc64'], etag='"exact-source-etag"', request_id='test-request')

    def get_object_acl(self, request):
        self.acls.append(request)
        return types.SimpleNamespace(acl=self.acl)


class CopyProductionTests(unittest.TestCase):
    def setUp(self):
        self.row = {'media_id': 'fixture', 'source_storage_key': 'legacy/'+'a'*64,
                    'object_key': tool.SOURCE_PREFIX+'legacy/'+'a'*64, 'bucket': 'fixture-bucket',
                    'size_bytes': 100, 'verified_source_sha256': 'b'*64, 'remote_crc64': '42',
                    'content_type': 'image/png', 'status': 'copied_verified'}

    @contextlib.contextmanager
    def sdk(self, client):
        sdk = types.SimpleNamespace(HeadObjectRequest=types.SimpleNamespace, CopyObjectRequest=types.SimpleNamespace,
                                    GetObjectAclRequest=types.SimpleNamespace)
        with patch.dict(sys.modules, alibabacloud_oss_v2=sdk), patch.object(tool, 'client', return_value=client):
            yield

    def test_copy_is_exact_private_conditional_and_forbids_overwrite(self):
        cl = CopyClient(self.row)
        with self.sdk(cl):
            result = tool.copy_one(self.row, {})
        request = cl.copies[0]
        self.assertEqual(request.key, tool.TARGET_PREFIX+self.row['source_storage_key'])
        self.assertEqual(request.source_key, self.row['object_key'])
        self.assertEqual(request.if_match, '"exact-source-etag"')
        self.assertTrue(request.forbid_overwrite)
        self.assertEqual(request.acl, 'private')
        self.assertEqual(result['copied_crc64'], '42')
        self.assertEqual(len(cl.heads), 2)
        self.assertEqual(len(cl.acls), 1)

    def test_wrapped_existing_object_still_requires_metadata_and_acl(self):
        cl = CopyClient(self.row, conflict=True)
        with self.sdk(cl):
            result = tool.copy_one(self.row, {})
            self.assertEqual(result['status'], 'existing_verified')
            self.assertEqual(result['copied_crc64'], '')
            cl.target_sha = 'c'*64
            with self.assertRaisesRegex(ValueError, 'head_sha_changed'):
                tool.copy_one(self.row, {})
            cl.target_sha, cl.acl = self.row['verified_source_sha256'], 'public-read'
            with self.assertRaisesRegex(ValueError, 'target_acl_not_private'):
                tool.copy_one(self.row, {})

    def test_saved_ledger_does_not_skip_current_verification(self):
        cl = CopyClient(self.row)
        with self.sdk(cl):
            previous = tool.copy_one(self.row, {})
            before = len(cl.copies)
            result = tool.copy_one(self.row, {}, previous)
            self.assertEqual(len(cl.copies), before)
            self.assertEqual(len(cl.heads), 4)
            self.assertEqual(len(cl.acls), 2)
            self.assertEqual(result['status'], 'existing_verified')
            changed = dict(previous, source_storage_key='legacy/'+'d'*64)
            with self.assertRaisesRegex(ValueError, 'ledger_source_changed'):
                tool.copy_one(self.row, {}, changed)

    def test_other_wrapped_failures_are_not_treated_as_existing(self):
        cl = CopyClient(self.row)
        with self.sdk(cl), patch.object(cl, 'copy_object', side_effect=WrappedFailure(ServiceFailure(403, 'AccessDenied'))):
            with self.assertRaises(WrappedFailure):
                tool.copy_one(self.row, {})
        self.assertEqual(cl.acls, [])

    def test_error_summary_does_not_emit_exception_text(self):
        error = WrappedFailure(ServiceFailure(403, 'AccessDenied'))
        summary = tool.error_summary(error)
        self.assertEqual(summary['code'], 'AccessDenied')
        self.assertEqual(summary['http_status'], 403)
        self.assertNotIn('error', summary)

    def test_product_order_binding_changes_with_association_or_order(self):
        original = {'m': {'product_id': 'product-a', 'sort_order': 1}}
        baseline = tool.digest(tool.order_manifest(original))
        self.assertNotEqual(baseline, tool.digest(tool.order_manifest({'m': {'product_id': 'product-b', 'sort_order': 1}})))
        self.assertNotEqual(baseline, tool.digest(tool.order_manifest({'m': {'product_id': 'product-a', 'sort_order': 2}})))

    def test_retired_whole_copy_rejects_apply_before_reading_secrets(self):
        with patch.object(tool, 'environment') as environment:
            with self.assertRaisesRegex(ValueError, 'whole_copy_disabled_by_40gb_capacity_constraint'):
                tool.run(types.SimpleNamespace(apply=True))
            environment.assert_not_called()


if __name__ == '__main__':
    unittest.main()
