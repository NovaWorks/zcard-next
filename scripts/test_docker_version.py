"""Docker installer version migration without touching host services."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DockerVersionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="zcard-docker-version-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for name in ("deploy", "server", "bin"):
            (self.root / name).mkdir()
        shutil.copy2(ROOT / "deploy/docker-install.sh", self.root / "deploy/docker-install.sh")
        self.history = self.root / "server/CHANGELOG.json"
        self.history.write_text(json.dumps([{"version": "v1.2.58"}]))
        self.trace = self.root / "trace"
        docker = self.root / "bin/docker"
        docker.write_text('#!/bin/sh\nprintf "%s|%s\\n" "$ZCARD_VERSION/$ZCARD_DOCKERFILE" "$*" >> "$TRACE"\ncase "$*" in *"port zcard"*) echo "127.0.0.1:18000";; esac\n')
        docker.chmod(0o755)
        curl = self.root / "bin/curl"
        curl.write_text("#!/bin/sh\ncase \"$*\" in *releases/latest*) echo '{\"tag_name\":\"v1.2.60\"}';; *) printf '{\"version\":\"%s\",\"status\":{\"database\":true,\"server\":true}}\\n' \"${HEALTH_VERSION:-$ZCARD_VERSION}\";; esac\n")
        curl.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.root / "bin") + ":" + os.environ["PATH"], TRACE=str(self.trace))
        self.env.pop("ZCARD_VERSION", None)

    def run_install(self, source=True):
        return subprocess.run(["bash", str(self.root / "deploy/docker-install.sh"), *(["--source"] if source else [])], env=self.env, capture_output=True, text=True)

    def test_existing_env_is_preserved_and_build_follows_source(self):
        env_file = self.root / "deploy/.env"
        for old in ("dev", "v1.2.57"):
            with self.subTest(old=old):
                original = f"ZCARD_VERSION={old}\nZCARD_JWT_ADMIN_KEY=keep-existing-key\nZCARD_PORT=8123\n"
                env_file.write_text(original)
                result = self.run_install()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(env_file.read_text(), original)
                self.assertIn("v1.2.58/deploy/Dockerfile|compose --env-file", self.trace.read_text())
        self.history.write_text(json.dumps([{"version": "v1.2.59"}]))
        self.trace.unlink()
        self.assertEqual(self.run_install().returncode, 0)
        self.assertIn("v1.2.59/deploy/Dockerfile|compose --env-file", self.trace.read_text())
        self.assertEqual(env_file.read_text(), original)

    def test_first_install_records_source_version_and_reuses_keys(self):
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        env_file = self.root / "deploy/.env"
        original = env_file.read_bytes()
        self.assertIn(b"ZCARD_VERSION=v1.2.58\n", original)
        self.assertEqual(self.run_install().returncode, 0)
        self.assertEqual(env_file.read_bytes(), original)

    def test_explicit_mismatch_or_invalid_source_cannot_build(self):
        self.env["ZCARD_VERSION"] = "v9.9.9"
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertNotIn("up -d", self.trace.read_text())
        self.env.pop("ZCARD_VERSION")
        self.history.write_text(json.dumps([{"version": "dev"}]))
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertNotIn("up -d", self.trace.read_text())

    def test_release_default_ignores_old_env_and_needs_no_source(self):
        self.history.unlink()
        env_file = self.root / "deploy/.env"
        original = "ZCARD_VERSION=dev\nZCARD_JWT_ADMIN_KEY=keep-existing-key\n"
        env_file.write_text(original)
        result = self.run_install(source=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        trace = self.trace.read_text()
        self.assertIn("v1.2.60/deploy/Dockerfile.release|compose --env-file", trace)
        self.assertLess(trace.index("build zcard"), trace.index("up -d --no-build"))
        self.assertEqual(env_file.read_text(), original)

    def test_release_can_pin_a_different_version_than_source(self):
        self.env["ZCARD_VERSION"] = "v1.2.59"
        result = self.run_install(source=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("v1.2.59/deploy/Dockerfile.release|compose --env-file", self.trace.read_text())

    def test_failed_download_build_does_not_recreate_containers(self):
        docker = self.root / "bin/docker"
        docker.write_text(docker.read_text()+"case \"$*\" in *'build zcard'*) exit 1;; esac\n")
        result = self.run_install(source=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("build zcard", self.trace.read_text())
        self.assertNotIn("up -d", self.trace.read_text())


if __name__ == "__main__":
    unittest.main()
