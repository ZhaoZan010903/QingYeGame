"""Verify completeness includes each product's ordered associations, not just files."""
import contextlib
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import check_legacy_media as checker


class GalleryVerificationTests(unittest.TestCase):
    def expected(self):
        return {'source-one': ['legacy/a', 'legacy/b'], 'source-two': ['legacy/c']}

    def rows(self):
        return [
            {'legacy_id': 'source-one', 'images': [
                {'sort_order': 1, 'storage_key': 'legacy/a'},
                {'sort_order': 2, 'storage_key': 'legacy/b'}]},
            {'legacy_id': 'source-two', 'images': [{'sort_order': 1, 'storage_key': 'legacy/c'}]},
        ]

    def test_complete_ordered_galleries_match(self):
        self.assertEqual(checker.compare_galleries(self.expected(), self.rows()), (2, []))

    def test_missing_link_fails_even_when_all_originals_exist(self):
        rows = self.rows()
        rows[0]['images'].pop()
        matched, issues = checker.compare_galleries(self.expected(), rows)
        self.assertEqual(matched, 1)
        self.assertEqual(issues[0]['error'], 'gallery_association_or_order_mismatch')
        self.assertNotIn('source-one', json.dumps(issues))

    def test_swapped_images_and_noncontiguous_positions_fail(self):
        for positions, keys in [([1, 2], ['legacy/b', 'legacy/a']), ([1, 3], ['legacy/a', 'legacy/b'])]:
            rows = self.rows()
            rows[0]['images'] = [dict(sort_order=position, storage_key=key) for position, key in zip(positions, keys)]
            self.assertEqual(checker.compare_galleries(self.expected(), rows)[0], 1)

    def test_missing_unexpected_and_duplicate_products_fail(self):
        rows = self.rows()[1:]
        rows.append({'legacy_id': 'unexpected', 'images': []})
        rows.append(rows[0])
        _, issues = checker.compare_galleries(self.expected(), rows)
        self.assertEqual({item['error'] for item in issues}, {
            'missing_database_product', 'unexpected_database_product', 'duplicate_database_product'})

    def test_source_rules_match_importer(self):
        products = [{'is_del': True}, {'_id': 'one', 'content':
            '//cdn.xuansiwei.com/a.png?resize,w_800,https://cdn.xuansiwei.com/b.png,'
            'https://cdn.xuansiwei.com/a.png?resize,w_800'}]
        galleries, raw_count = checker.source_galleries(products)
        self.assertEqual(raw_count, 3)
        self.assertEqual(len(galleries['one']), 2)
        with self.assertRaises(ValueError):
            checker.source_galleries([{'_id': 'one'}, {'_id': 'one'}])

    def test_end_to_end_rehash_and_association_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            url = 'https://cdn.xuansiwei.com/test/shared.png'
            products = [{'_id': 'one', 'content': url}, {'_id': 'two', 'content': url}]
            galleries, _ = checker.source_galleries(products)
            key = galleries['one'][0]
            payload = b'fixture-original-bytes'
            digest = hashlib.sha256(payload).hexdigest()
            (root / key).parent.mkdir()
            (root / key).write_bytes(payload)
            (root / 'source.json').write_text(json.dumps(products))
            (root / 'manifest.jsonl').write_text(json.dumps({
                'key': key, 'state': 'ready', 'bytes': len(payload), 'sha256': digest}) + '\n')
            media = {'storage_key': key, 'status': 'ready', 'byte_size': len(payload), 'sha256': digest}
            rows = [{'legacy_id': product['_id'], 'images': [{'sort_order': 1, 'storage_key': key}]}
                    for product in products]
            arguments = ['check', '--source', str(root / 'source.json'), '--manifest', str(root / 'manifest.jsonl'),
                         '--media-root', str(root), '--output', str(root / 'report.json'), '--rehash']

            def check():
                result = type('Result', (), {'stdout': json.dumps({'media': [media, media], 'products': rows})})()
                with patch('sys.argv', arguments), patch.object(checker.subprocess, 'run', return_value=result), \
                        contextlib.redirect_stdout(io.StringIO()):
                    status = checker.main()
                return status, json.loads((root / 'report.json').read_text())

            status, report = check()
            self.assertEqual(status, 0)
            self.assertEqual(report['verified_product_galleries'], 2)
            self.assertEqual(report['verified_originals'], 1)
            self.assertEqual(report['shared_original_media_rows'], 1)
            rows[0]['images'] = []
            status, report = check()
            self.assertEqual(status, 1)
            self.assertFalse(report['passed'])
            self.assertEqual(report['verified_originals'], 1)
            self.assertEqual(report['issue_count'], 1)
            self.assertNotIn(url, json.dumps(report))


if __name__ == '__main__':
    unittest.main()
