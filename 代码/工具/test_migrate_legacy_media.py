"""Exercise resumability, original-byte integrity, URL parsing and the disk guard."""
import base64
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import urllib.request
import migrate_legacy_media as migration

PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aHfoAAAAASUVORK5CYII=')

class Response(io.BytesIO):
    def __init__(self):
        super().__init__(PNG)
        self.headers = {'Content-Length': str(len(PNG)), 'Content-Type': 'image/png'}

class MigrationTests(unittest.TestCase):
    def run_job(self, directory, urls, free_bytes=20 * 1024**3, reject_first=False):
        source = directory/'source.json'
        source.write_text(json.dumps([{'content': ','.join(urls)}]), encoding='utf-8')
        arguments = ['migrate', '--source', str(source), '--media-root', str(directory/'media'),
                     '--manifest', str(directory/'manifest.jsonl'), '--status', str(directory/'status.json'),
                     '--workers', '2', '--rate', '1000000']
        requests = []
        def fetch(request, **kwargs):
            requests.append(request.full_url)
            if reject_first and len(requests) == 1:
                raise urllib.error.HTTPError(request.full_url, 400, 'Bad request', {}, io.BytesIO())
            return Response()
        disk = type('Disk', (), {'free': free_bytes})()
        with patch('sys.argv', arguments), patch.object(migration, 'open_legacy_image', fetch), \
             patch.object(migration.shutil, 'disk_usage', return_value=disk), \
             patch.object(migration.time, 'sleep', return_value=None):
            migration.main()
        return requests, json.loads((directory/'status.json').read_text())

    def test_original_urls_with_spaces_commas_and_resume(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            url = 'https://cdn.xuansiwei.com/中文 image.png?x-oss-process=image/resize,w_800'
            requests, status = self.run_job(root, [url, url])
            key = 'legacy/' + hashlib.sha256(url.encode()).hexdigest()
            self.assertEqual((root/'media'/key).read_bytes(), PNG)
            self.assertIn('%20', requests[0])
            self.assertIn('resize,w_800', requests[0])
            self.assertEqual(status['total'], 1)
            self.assertTrue(status['complete'])
            requests, status = self.run_job(root, [url])
            self.assertEqual(requests, [])
            self.assertEqual(status['resumed'], 1)
            (root/'media'/key).write_bytes(b'broken')
            requests, status = self.run_job(root, [url])
            self.assertEqual(len(requests), 1)
            self.assertEqual((root/'media'/key).read_bytes(), PNG)

    def test_disk_guard_stops_before_writing(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with self.assertRaises(SystemExit) as error:
                self.run_job(root, ['https://cdn.xuansiwei.com/test.png'], free_bytes=4 * 1024**3)
            self.assertEqual(error.exception.code, 75)
            status = json.loads((root/'status.json').read_text())
            self.assertEqual(status['stopReason'], 'insufficient_disk')
            self.assertFalse(status['complete'])
            self.assertEqual(list((root/'media/legacy').iterdir()), [])

    def test_unapproved_source_is_never_requested(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with self.assertRaises(SystemExit) as error:
                self.run_job(root, ['http://127.0.0.1/private.png'])
            self.assertEqual(error.exception.code, 1)
            self.assertEqual(json.loads((root/'status.json').read_text())['failed'], 1)

    def test_encoded_path_is_not_double_encoded_when_first_request_succeeds(self):
        with tempfile.TemporaryDirectory() as temp:
            url = 'https://cdn.xuansiwei.com/test/encoded%20name.png?x-oss-process=image/resize,w_800'
            requests, status = self.run_job(Path(temp), [url])
            self.assertEqual(requests, [url])
            self.assertTrue(status['complete'])

    def test_literal_percent_fallback_keeps_original_key_and_query(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            url = 'https://cdn.xuansiwei.com/test/literal%C5W2.png?process=keep%20query'
            requests, status = self.run_job(root, [url], reject_first=True)
            self.assertEqual(requests, [url, url.replace('literal%C5', 'literal%25C5')])
            key = 'legacy/' + hashlib.sha256(url.encode()).hexdigest()
            self.assertEqual((root/'media'/key).read_bytes(), PNG)
            entry = json.loads((root/'manifest.jsonl').read_text().strip())
            self.assertEqual(entry['source_url'], url)
            self.assertEqual(entry['key'], key)
            self.assertTrue(status['complete'])
            self.assertEqual(status['failed'], 0)

    def test_redirect_cannot_leave_approved_https_cdn(self):
        handler = migration.ApprovedCDNRedirect()
        request = urllib.request.Request('https://cdn.xuansiwei.com/test.png')
        for target in ('http://cdn.xuansiwei.com/next.png', 'https://127.0.0.1/private',
                       'https://other.invalid/a.png', 'https://cdn.xuansiwei.com:444/a.png',
                       'https://user@cdn.xuansiwei.com/a.png'):
            with self.subTest(target=target), self.assertRaises(urllib.error.HTTPError) as error:
                handler.redirect_request(request, None, 302, 'Found', {}, target)
            error.exception.close()
        allowed = handler.redirect_request(request, None, 302, 'Found', {},
                                           'https://cdn.xuansiwei.com/next.png')
        self.assertEqual(allowed.full_url, 'https://cdn.xuansiwei.com/next.png')

    def test_http_source_is_requested_over_https(self):
        initial, _ = migration.request_urls('http://cdn.xuansiwei.com/test.png')
        self.assertEqual(initial, 'https://cdn.xuansiwei.com/test.png')

if __name__ == '__main__':
    unittest.main()
