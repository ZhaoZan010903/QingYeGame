"""证书切换故障与部署边界测试；不连接服务器、不启动真实服务。"""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest import mock


# 运维脚本只在 Linux 执行；Windows 可验证相同的状态切换和失败回退逻辑。
stubs = {}
for name in ('fcntl', 'grp'):
    try:
        __import__(name)
    except ImportError:
        stubs[name] = types.ModuleType(name)
spec = importlib.util.spec_from_file_location('qingye_edge_ip_cert', Path(__file__).with_name('deploy-ip-certificate.py'))
module = importlib.util.module_from_spec(spec)
with mock.patch.dict(sys.modules, stubs):
    spec.loader.exec_module(module)


class CertificateTransitions(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name).resolve()
        self.base = root / 'tls'
        self.base.mkdir()
        self.lineage = root / 'live'
        self.lineage.mkdir()
        self.previous = self.base / 'cert-previous'
        self.previous.mkdir()
        for name in module.FILES:
            (self.previous / name).write_bytes(('old:' + name).encode())
            (self.lineage / name).write_bytes(('new:' + name).encode())
        self.start(mock.patch.object(module, 'BASE', self.base))
        self.start(mock.patch.object(module, 'LINEAGE', self.lineage))
        self.current = self.previous
        self.points = []
        self.start(mock.patch.object(module, 'previous_certificate', return_value=self.previous))
        self.start(mock.patch.object(module, 'point_to', side_effect=self.point_to))
        self.start(mock.patch.object(module, 'sync_directory'))
        self.start(mock.patch.object(module.os, 'chown', create=True))
        self.start(mock.patch.object(module.os, 'fchown', create=True))
        self.start(mock.patch.object(module.os, 'fchmod', create=True))
        self.run = self.start(mock.patch.object(module, 'run'))
        self.validate = self.start(mock.patch.object(module, 'validate_pair'))
        self.activate = self.start(mock.patch.object(module, 'activate'))
        self.health = self.start(mock.patch.object(module, 'check_api_health'))

    def start(self, patcher):
        value = patcher.start()
        self.addCleanup(patcher.stop)
        return value

    def point_to(self, path):
        # 测试平台无需创建 Windows 特权符号链接；保留真实候选/回退文件。
        self.current = path
        self.points.append(path)

    def test_success_keeps_complete_pair_and_previous(self):
        module.install_locked(123, False)
        self.assertNotEqual(self.current, self.previous)
        self.assertTrue(self.previous.is_dir())
        for name in module.FILES:
            self.assertEqual((self.current / name).read_bytes(), (self.lineage / name).read_bytes())
        self.activate.assert_called_once_with()

    def test_same_certificate_does_not_reload_or_switch(self):
        for name in module.FILES:
            (self.lineage / name).write_bytes((self.previous / name).read_bytes())
        module.install_locked(123, False)
        self.assertEqual(list(self.base.iterdir()), [self.previous])
        self.assertEqual(self.points, [])
        self.activate.assert_not_called()

    def test_invalid_candidate_never_replaces_current(self):
        self.validate.side_effect = RuntimeError('invalid certificate')
        with self.assertRaisesRegex(RuntimeError, 'invalid certificate'):
            module.install_locked(123, False)
        self.assertEqual(self.current, self.previous)
        self.assertEqual(list(self.base.iterdir()), [self.previous])
        self.activate.assert_not_called()

    def test_failed_activation_restores_old_pair_and_retains_evidence(self):
        self.activate.side_effect = RuntimeError('edge reload failed')
        with self.assertRaisesRegex(RuntimeError, '已恢复原有证书状态'):
            module.install_locked(123, False)
        self.assertEqual(self.current, self.previous)
        self.assertEqual(len(self.points), 2)
        self.assertTrue(self.points[0].exists())
        for service in module.API_SERVICES:
            self.assertIn(mock.call('systemctl', 'restart', service), self.run.call_args_list)
        self.health.assert_called_once_with()

    def test_rollback_attempts_both_apis_even_if_edge_test_fails(self):
        self.activate.side_effect = RuntimeError('activation failed')
        def commands(*command):
            if command[0] == module.NGINX:
                raise RuntimeError('config failed')
        self.run.side_effect = commands
        with self.assertRaisesRegex(RuntimeError, '部分服务恢复失败'):
            module.install_locked(123, False)
        self.assertEqual(self.current, self.previous)
        for service in module.API_SERVICES:
            self.assertIn(mock.call('systemctl', 'restart', service), self.run.call_args_list)
        self.health.assert_called_once_with()

    def test_missing_source_key_never_switches(self):
        (self.lineage / 'privkey.pem').unlink()
        with self.assertRaises(FileNotFoundError):
            module.install_locked(123, False)
        self.assertEqual(self.points, [])
        self.assertEqual(list(self.base.iterdir()), [self.previous])

    def test_install_only_refuses_running_services(self):
        with mock.patch.object(module, 'any_services_active', return_value=True):
            with self.assertRaisesRegex(RuntimeError, '均未运行'):
                module.install_locked(123, True)
        self.assertEqual(self.points, [])
        self.activate.assert_not_called()

    def test_install_only_initial_pair_does_not_operate_services(self):
        with mock.patch.object(module, 'previous_certificate', return_value=None), \
             mock.patch.object(module, 'any_services_active', return_value=False):
            module.install_locked(123, True)
        self.assertEqual(len(self.points), 1)
        self.activate.assert_not_called()
        self.run.assert_not_called()

    def test_active_install_requires_previous_pair(self):
        with mock.patch.object(module, 'previous_certificate', return_value=None):
            with self.assertRaisesRegex(RuntimeError, '首次安装'):
                module.install_locked(123, False)
        self.assertEqual(self.points, [])


class CertificateValidation(unittest.TestCase):
    def test_wrong_lineage_rejected_before_filesystem_work(self):
        with mock.patch.object(module.os, 'geteuid', return_value=0, create=True), \
             mock.patch.dict(os.environ, {'RENEWED_LINEAGE': '/etc/another-site/live/certificate'}), \
             mock.patch.object(module, 'install_locked') as install:
            with self.assertRaisesRegex(RuntimeError, '其他站点'):
                module.install()
            install.assert_not_called()

    def test_non_root_rejected(self):
        with mock.patch.object(module.os, 'geteuid', return_value=1000, create=True):
            with self.assertRaisesRegex(RuntimeError, 'root'):
                module.install()

    def test_key_mismatch_rejected_after_identity_chain_checks(self):
        with mock.patch.object(module, 'run') as run, \
             mock.patch.object(module, 'public_key', side_effect=[b'cert-key', b'wrong-key']):
            with self.assertRaisesRegex(RuntimeError, '不匹配'):
                module.validate_pair(Path('/candidate'))
            self.assertTrue(any('-checkip' in c.args and '8.140.230.149' in c.args for c in run.call_args_list))
            self.assertTrue(any('-verify_ip' in c.args for c in run.call_args_list))
            self.assertTrue(any('-checkend' in c.args and '86400' in c.args for c in run.call_args_list))

    def test_valid_pair_checks_match(self):
        with mock.patch.object(module, 'run'), \
             mock.patch.object(module, 'public_key', return_value=b'identical-public-key'):
            module.validate_pair(Path('/candidate'))

    def test_activation_uses_only_edge_and_two_apis(self):
        with mock.patch.object(module, 'run') as run, \
             mock.patch.object(module, 'check_api_health') as health:
            module.activate()
            calls = run.call_args_list
            self.assertEqual(calls[0], mock.call(module.NGINX, '-t', '-p', '/var/lib/qingye-edge/', '-c', '/etc/qingye-edge/nginx.conf'))
            self.assertEqual(calls[1], mock.call('systemctl', 'reload', 'qingye-edge.service'))
            self.assertEqual([c.args[2] for c in calls if c.args[:2] == ('systemctl', 'restart')], list(module.API_SERVICES))
            self.assertNotIn('/www/server', repr(calls))
            self.assertNotIn('qingye-production-web.service', repr(calls))
            health.assert_called_once_with()

    def test_bad_nginx_config_prevents_activation(self):
        with mock.patch.object(module, 'run', side_effect=RuntimeError('bad config')) as run, \
             mock.patch.object(module, 'check_api_health') as health:
            with self.assertRaisesRegex(RuntimeError, 'bad config'):
                module.activate()
            self.assertEqual(run.call_count, 1)
            health.assert_not_called()

    def test_install_only_guard_detects_reloading(self):
        with mock.patch.object(module.subprocess, 'run', return_value=types.SimpleNamespace(stdout='reloading\n')):
            self.assertTrue(module.any_services_active())

    def test_install_only_guard_allows_only_inactive_or_failed(self):
        results = [types.SimpleNamespace(stdout=s) for s in ('inactive\n', 'failed\n', 'inactive\n')]
        with mock.patch.object(module.subprocess, 'run', side_effect=results):
            self.assertFalse(module.any_services_active())

    def test_regular_current_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            (base / 'current').write_text('not a link')
            with mock.patch.object(module, 'BASE', base):
                with self.assertRaisesRegex(RuntimeError, '不是符号链接'):
                    module.previous_certificate()

    def test_pointer_rejects_external_target(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            outside = base / 'other' / 'cert'
            outside.mkdir(parents=True)
            with mock.patch.object(module, 'BASE', base):
                with self.assertRaisesRegex(RuntimeError, '目标证书'):
                    module.point_to(outside)


if __name__ == '__main__':
    unittest.main()
