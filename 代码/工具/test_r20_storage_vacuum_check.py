"""Local safety tests; no server, credentials, or storage mutation is used."""
import argparse
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("storage_check", Path(__file__).with_name("r20_storage_vacuum_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


def volume_line(volume_id=1, collection=check.BUCKET):
    return f"volume Id:{volume_id}, Size:50000, ReplicaPlacement:000, Collection:{collection}, Version:3, FileCount:14, DeleteCount:0, DeletedByteCount:0"


class StorageCheckSafetyTests(unittest.TestCase):
    def test_real_client_rejects_non_acceptance_bucket(self):
        for environment, bucket in (("test", check.BUCKET), ("acceptance", "qingye-media")):
            with mock.patch.dict(check.os.environ, {"MEDIA_R20_ENVIRONMENT": environment, "MEDIA_STAGE_BUCKET": bucket}), \
                    self.assertRaises(check.CheckFailure):
                check.S3()

    def test_loopback_and_endpoint_injection(self):
        self.assertEqual(check.loopback_origin("http://127.0.0.1:8333/"), "http://127.0.0.1:8333")
        for endpoint in ("https://oss-cn-beijing.aliyuncs.com", "http://example.com:8333", "http://127.0.0.1",
                         "http://127.0.0.1:8333/path", "http://a:b@127.0.0.1:8333", "http://127.0.0.1:8333?x=1"):
            with self.subTest(endpoint=endpoint), self.assertRaises(check.CheckFailure):
                check.loopback_origin(endpoint)

    def test_duplicate_ids_rejected_even_if_acceptance_is_present(self):
        with self.assertRaises(check.CheckFailure):
            check.parse_volumes(volume_line() + "\n" + volume_line(1, "qingye-media"))

    def test_only_exact_local_collection_is_allowed(self):
        check.validate_volume(check.parse_volumes(volume_line()), 1)
        for text, target in ((volume_line(), 2), (volume_line(1, "qingye-acceptance-other"), 1),
                             (volume_line() + ", RemoteStorageName:oss", 1)):
            with self.subTest(text=text), self.assertRaises(check.CheckFailure):
                check.validate_volume(check.parse_volumes(text), target)
        with self.assertRaises(check.CheckFailure):
            check.parse_volumes("unexpected old format")

    def test_other_collection_filename_is_not_excluded_by_prefix(self):
        self.assertTrue(check.ACCEPTANCE_FILE.fullmatch("qingye-acceptance_1.dat"))
        self.assertFalse(check.ACCEPTANCE_FILE.fullmatch("qingye-acceptance_other_1.dat"))
        self.assertFalse(check.ACCEPTANCE_FILE.fullmatch("qingye-acceptance_1.cpd"))

    def test_deletions_cannot_address_bucket_directory_or_existing_product(self):
        client = check.S3.__new__(check.S3)
        for key in ("", "r20/acceptance/objects/", "r20/acceptance/objects/product/generation/original",
                    "r20/test/objects/vacuum" + "a" * 32 + "/probe01/original",
                    "r20/acceptance/objects/vacuum" + "a" * 32 + "/probe01/original/"):
            with self.subTest(key=key), self.assertRaises(check.CheckFailure):
                client.request("DELETE", key)
        key = "r20/acceptance/objects/vacuum" + "a" * 32 + "/probe01/original"
        with self.assertRaises(check.CheckFailure):
            client.request("DELETE", key, query={"delete": ""})
        with self.assertRaises(check.CheckFailure):
            client.request("PUT", key, payload=b"too small")

    def test_small_real_png_fixture(self):
        payload = check.png_bytes()
        self.assertTrue(payload.startswith(b"\x89PNG\r\n\x1a\n"))
        self.assertGreaterEqual(len(payload), 1024)
        self.assertLessEqual(len(payload), 10 * 1024)

    def test_vacuum_budget_includes_headroom_and_workspace(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            (directory / "qingye-acceptance_1.dat").write_bytes(b"x" * 4096)
            (directory / "qingye-acceptance_1.idx").write_bytes(b"x" * 64)
            with mock.patch.object(check, "snapshot", return_value={"available_bytes": check.RESERVE + 512 * check.MIB}):
                result = check.vacuum_budget(directory, 1)
                self.assertEqual(result["required_available_bytes"], check.RESERVE + 512 * check.MIB)
            with mock.patch.object(check, "snapshot", return_value={"available_bytes": check.RESERVE}):
                with self.assertRaises(check.CheckFailure):
                    check.vacuum_budget(directory, 1)

    def test_default_preflight_never_creates_s3_client_or_locks(self):
        with tempfile.TemporaryDirectory() as temp:
            args = argparse.Namespace(master="127.0.0.1:9333", volume_dir=temp, weed="weed", exercise=False, vacuum=False)
            result = {}
            with mock.patch.object(check.sys, "platform", "linux"), \
                    mock.patch.object(check.shutil, "which", return_value="/usr/local/bin/weed"), \
                    mock.patch.object(check.subprocess, "run", return_value=mock.Mock(stdout="version 4.53", stderr="")), \
                    mock.patch.object(check, "snapshot", return_value={"available_bytes": check.RESERVE * 3, "files": {}}), \
                    mock.patch.object(check, "shell", return_value=volume_line()) as shell, \
                    mock.patch.object(check, "S3", side_effect=AssertionError("read-only preflight instantiated S3")):
                check.run(args, result)
            self.assertEqual(result["status"], "read_only_preflight_passed")
            self.assertEqual(shell.call_args.args[2], ["volume.list"])

    def test_shell_failures_preserve_private_diagnostics(self):
        with mock.patch.object(check.subprocess, "run", return_value=mock.Mock(returncode=1, stdout="error: volume absent", stderr="")):
            with self.assertRaises(check.ShellCheckFailure) as result:
                check.shell("weed", "127.0.0.1:9333", ["volume.list"])
        self.assertIn("volume absent", result.exception.output)

    def test_wait_checks_deletion_counters_before_returning(self):
        initial = volume_line(2)
        propagated = initial.replace("DeleteCount:0", "DeleteCount:12").replace("DeletedByteCount:0", "DeletedByteCount:38088")
        clock = [0.0]
        with mock.patch.object(check, "shell", side_effect=[initial, initial, propagated]) as shell, \
                mock.patch.object(check.time, "monotonic", side_effect=lambda: clock[0]), \
                mock.patch.object(check.time, "sleep", side_effect=lambda seconds: clock.__setitem__(0, clock[0] + seconds)):
            result = check.wait_for_delete_propagation("weed", "127.0.0.1:9333", 2, {"deleted_count": 0, "deleted_bytes": 0})
        self.assertTrue(result["observed"])
        self.assertEqual(len(result["samples"]), 3)
        self.assertEqual(clock[0], 6)
        self.assertTrue(all(call.args[2] == ["volume.list"] for call in shell.call_args_list))

    def test_wait_times_out_without_mutating_storage(self):
        clock = [0.0]
        with mock.patch.object(check, "shell", return_value=volume_line(2)) as shell, \
                mock.patch.object(check.time, "monotonic", side_effect=lambda: clock[0]), \
                mock.patch.object(check.time, "sleep", side_effect=lambda seconds: clock.__setitem__(0, clock[0] + seconds)):
            result = check.wait_for_delete_propagation("weed", "127.0.0.1:9333", 2, {"deleted_count": 0, "deleted_bytes": 0})
        self.assertFalse(result["observed"])
        self.assertEqual(clock[0], 45)
        self.assertEqual(shell.call_count, 15)
        self.assertTrue(all(call.args[2] == ["volume.list"] for call in shell.call_args_list))


if __name__ == "__main__":
    unittest.main()
