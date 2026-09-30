"""Failure/recovery contracts for the persistent Docker update controller."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("docker_updater", ROOT / "deploy/docker-updater.py")
updater = importlib.util.module_from_spec(spec)
spec.loader.exec_module(updater)


def release(version, schema="a"):
    return {"version": version, "protocol": 1, "image": updater.metadata.APP_REPO + "@sha256:" + "1" * 64,
            "updater_image": updater.metadata.UPDATER_REPO + "@sha256:" + "2" * 64,
            "schema": {k: schema * 64 for k in ("sqlite", "mysql", "postgres")}}


class FakeController(updater.Controller):
    def __init__(self, root):
        super().__init__(root, root, "isolated-project", "helper")
        self.events = []
        self.inject = ""

    def compose(self, *args, **kwargs):
        self.events.append(args)
        return ""

    def command(self, args, **kwargs):
        self.events.append(tuple(args))
        if self.inject == "pull" and args[:2] == ["docker", "pull"]:
            raise RuntimeError("pull failed")
        return '[{"Id":"old-image"}]'

    def container(self):
        return {"Image": "old-image", "State": {"Running": False, "ExitCode": 0}}, "original-volume"

    def health(self, version=None):
        return {"version": version, "dialect": "sqlite"}

    def wait_health(self, version, timeout=180):
        self.events.append(("health", version))
        if self.inject == "health" and version == "v1.2.96":
            raise RuntimeError("new version failed")

    def backup(self, volume):
        self.events.append(("backup", volume))
        if self.inject == "backup":
            raise RuntimeError("backup failed")


class ControllerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.c = FakeController(Path(self.tmp.name))
        updater.metadata.atomic_json(self.c.state / "current.json", release("v1.2.95"))
        self.c.job = {"id": "test", "target": "v1.2.96", "previous": "v1.2.95", "dialect": "sqlite", "busy": True}
        self.target = release("v1.2.96")
        self.resolver = patch.object(updater.metadata, "resolve", side_effect=lambda _: self.target)
        self.resolver.start()
        self.addCleanup(self.resolver.stop)

    def test_success_persists_desired_and_previous_versions(self):
        self.c.run()
        self.assertFalse(self.c.job["busy"])
        self.assertEqual(self.c.job["phase"], "idle")
        self.assertEqual(self.c.read("current.json")["version"], "v1.2.96")
        self.assertEqual(self.c.read("previous.json")["version"], "v1.2.95")
        self.assertEqual(self.c.read("image.json")["services"]["zcard"]["image"], self.target["image"])
        self.assertTrue(self.c.status()["rollback_ready"])
        self.assertLess(self.c.events.index(("stop", "-t", "60", "zcard")), self.c.events.index(("backup", "original-volume")))

    def test_download_failure_never_stops_existing_service(self):
        self.c.inject = "pull"
        self.c.run()
        self.assertEqual(self.c.job["phase"], "failed")
        self.assertFalse(any(x[0] in ("stop", "up", "backup") for x in self.c.events))

    def test_backup_failure_restarts_old_image_without_starting_new(self):
        self.c.inject = "backup"
        self.c.run()
        self.assertEqual(self.c.job["phase"], "rolled_back")
        self.assertNotIn(("health", "v1.2.96"), self.c.events)
        self.assertEqual(self.c.read("current.json")["version"], "v1.2.95")

    def test_unhealthy_compatible_release_restores_old_version(self):
        self.c.inject = "health"
        self.c.run()
        self.assertEqual(self.c.job["phase"], "rolled_back")
        self.assertIn(("health", "v1.2.95"), self.c.events)

    def test_migration_failure_does_not_run_old_code_on_new_schema(self):
        self.target = release("v1.2.96", "b")
        self.c.inject = "health"
        self.c.run()
        self.assertEqual(self.c.job["phase"], "failed")
        self.assertNotIn(("health", "v1.2.95"), self.c.events)
        self.assertIn("数据库可能已迁移", self.c.job["error"])

    def test_restart_after_new_start_can_complete_job(self):
        self.c.save(old=release("v1.2.95"), release=self.target, stopped=True, new_started=True, phase="verifying")
        restarted = FakeController(self.c.state)
        restarted.recover()
        self.assertEqual(restarted.read("current.json")["version"], "v1.2.96")
        self.assertFalse(restarted.job["busy"])

    def test_restart_during_backup_restores_old_without_overwriting_data(self):
        self.c.save(old=release("v1.2.95"), release=self.target, stopped=True, phase="backing_up")
        restarted = FakeController(self.c.state)
        restarted.recover()
        self.assertEqual(restarted.job["phase"], "rolled_back")
        self.assertEqual(restarted.read("current.json")["version"], "v1.2.95")

    def test_duplicate_apply_returns_existing_job(self):
        with patch.object(updater.threading, "Thread", side_effect=AssertionError("duplicate worker")):
            self.assertEqual(self.c.start("v1.2.96", "v1.2.95", "sqlite")["id"], "test")

    def test_invalid_version_and_external_database_fail_before_work(self):
        self.c.job["busy"] = False
        for version, dialect in (("v1.2.95;id", "sqlite"), ("v1.2.96", "postgres"), ("v1.2.94", "sqlite")):
            with self.assertRaises(ValueError):
                self.c.start(version, "v1.2.95", dialect)
        self.assertEqual(self.c.events, [])

    def test_untrusted_image_and_missing_schema_rejected(self):
        for change in ({"image": "attacker/image:latest"}, {"protocol": 2}, {"schema": {}}):
            with self.assertRaises(ValueError):
                updater.metadata.validate(dict(self.target, **change), "v1.2.96")


if __name__ == "__main__":
    unittest.main()
