"""Installer regressions without changing host services: python3 scripts/test_install.py."""
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest
from urllib.parse import urlsplit, unquote

ROOT = Path(__file__).resolve().parents[1]


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="zcard-installer-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.install = self.root / "install"
        self.binary = self.root / "zcard"
        self.binary.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$TRACE"\nexit "${BINARY_EXIT:-0}"\n')
        self.binary.chmod(0o755)
        self.env = dict(os.environ, ZCARD_INSTALL_DIR=str(self.install),
                        ZCARD_UNIT_FILE=str(self.root / "zcard.service"), TRACE=str(self.root / "trace"))
        for name in ("ZCARD_JWT_ADMIN_KEY", "ZCARD_JWT_USER_KEY", "ZCARD_CARD_KEY", "ZCARD_DATA_KEY"):
            self.env.pop(name, None)

    def run_script(self, args, fail=False):
        source = shlex.quote(str(ROOT / "scripts/zcard-install.sh"))
        # Service state is isolated; failure injection must propagate through the public CLI.
        script = f'''source {source}
need_root() {{ :; }}
have_systemd() {{ return 0; }}
ensure_backup_tools() {{ :; }}
sleep() {{ :; }}
journalctl() {{ :; }}
systemctl() {{ printf '%s\\n' "$*" >> "$TRACE"; {'[ "$1" = daemon-reload ]' if fail else ':'}; }}
curl() {{ echo '{{"status":{{"database":true,"server":true}}}}'; }}
main "$@"
'''
        return subprocess.run(["bash", "-c", script, "test", *args], env=self.env,
                              capture_output=True, text=True)

    def install_args(self, *extra):
        return ["install", "--bin", str(self.binary), *extra]

    def test_install_persists_independent_keys_and_enables_service(self):
        r = self.run_script(self.install_args("--db", "sqlite"))
        self.assertEqual(r.returncode, 0, r.stderr)
        path = self.install / "configs/config.yaml"
        cfg = json.loads(path.read_text())
        keys = list(cfg["security"].values())
        self.assertEqual(len(set(keys)), 4)
        self.assertTrue(all(len(bytes.fromhex(k)) == 32 for k in keys))
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(cfg["server"]["grpc"]["addr"], "")
        self.assertEqual(cfg["data"]["redis"]["addr"], "")
        trace = (self.root / "trace").read_text()
        self.assertIn("enable zcard.service", trace)
        self.assertIn("restart zcard.service", trace)
        before = path.read_bytes()
        r = self.run_script(self.install_args("--db", "sqlite"))
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(path.read_bytes(), before)

    def test_service_failure_is_not_success(self):
        r = self.run_script(self.install_args("--db", "sqlite"), fail=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn("安装完成", r.stdout)

    def test_db_passwords_survive_config_and_url_encoding(self):
        password = 'quote"slash\\hash#at@colon:percent%'
        r = self.run_script(self.install_args("--db", "postgres", "--db-user", "user@name",
            "--db-pass", password, "--redis-pass", password, "--redis", "redis:6379"))
        self.assertEqual(r.returncode, 0, r.stderr)
        cfg = json.loads((self.install / "configs/config.yaml").read_text())
        url = urlsplit(cfg["data"]["database"]["source"])
        self.assertEqual(unquote(url.username), "user@name")
        self.assertEqual(unquote(url.password), password)
        self.assertEqual(cfg["data"]["redis"]["password"], password)

    def test_rejects_bad_arguments_before_installing(self):
        for args in (["--unknown", "a"], ["--db"], ["--db", "wrong"]):
            r = self.run_script(self.install_args(*args))
            self.assertNotEqual(r.returncode, 0)
            self.assertFalse((self.install / "zcard").exists())

    def test_update_uses_verified_updater_and_propagates_failure(self):
        self.assertEqual(self.run_script(self.install_args("--db", "sqlite")).returncode, 0)
        before = (self.install / "zcard").read_bytes()
        r = self.run_script(["update", "--bin", str(self.binary)])
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual((self.install / "zcard").read_bytes(), before)
        self.env["BINARY_EXIT"] = "42"
        r = self.run_script(["update", "-source", "github"])
        self.assertEqual(r.returncode, 42)
        self.assertIn("self-update -y -conf", (self.root / "trace").read_text())

    def test_update_rollback_is_reported_as_failure(self):
        self.assertEqual(self.run_script(self.install_args("--db", "sqlite")).returncode, 0)
        installed = self.install / "zcard"
        installed.write_text("#!/bin/sh\nprintf '%s' '{\"status\":\"ok\",\"rolled_back\":true}' > update.state\n")
        r = self.run_script(["update"])
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("回滚", r.stderr)

    def test_checksum_mismatch_never_installs_binary(self):
        source = shlex.quote(str(ROOT / "scripts/zcard-install.sh"))
        payload = self.root / "payload"
        payload.write_bytes(b"untrusted binary")
        checksum = self.root / "SHA256SUMS"
        checksum.write_text("0" * 64 + "  zcard-linux-arm64\n")
        self.env["ZCARD_VERSION"] = "v1.2.18"
        script = f'''source {source}
arch_name() {{ echo arm64; }}
curl() {{
  local out=""
  while [ "$#" -gt 1 ]; do
    if [ "$1" = -o ]; then out="$2"; shift; fi
    shift
  done
  case "$1" in
    */SHA256SUMS) cp {shlex.quote(str(checksum))} "$out" ;;
    *) cp {shlex.quote(str(payload))} "$out" ;;
  esac
}}
download_bin "$1"
'''
        dest = self.root / "downloaded"
        r = subprocess.run(["bash", "-c", script, "test", str(dest)], env=self.env,
                           capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(dest.exists())
        self.assertIn("SHA256", r.stderr)

    def test_legacy_entrypoint_forwards_arguments(self):
        r = subprocess.run(["bash", str(ROOT / "deploy/install.sh"), "--help"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("ZCARD_INSTALL_DIR", r.stdout)


if __name__ == "__main__":
    unittest.main()
