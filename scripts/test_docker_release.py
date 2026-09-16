"""Signed release downloader contract tests using a local HTTP fixture."""
import base64
from functools import partial
import hashlib
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("download_release", ROOT / "deploy/download-release.py")
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


class ReleaseDownloadTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="zcard-download-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.web = self.root / "web"
        self.tag = "v1.2.60"
        self.files = self.web / self.tag
        self.files.mkdir(parents=True)
        self.private = self.root / "private.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ED25519", "-out", str(self.private)], check=True, capture_output=True)
        public = subprocess.check_output(["openssl", "pkey", "-in", str(self.private), "-pubout", "-outform", "DER"])
        self.public = public[-32:].hex()
        self.addCleanup(patch.stopall)
        patch.object(release, "PUBLIC_KEY", self.public).start()
        self.manifest = {"version": self.tag, "channel": "stable", "notes": "测试 <签名> & 校验\u2028", "files": [], "signature": ""}
        for arch in ("amd64", "arm64"):
            data = b"fixture-binary-" + arch.encode()
            name = "zcard-linux-" + arch
            (self.files / name).write_bytes(data)
            self.manifest["files"].append({"name": name, "size": len(data), "sha256": hashlib.sha256(data).hexdigest()})
        self.sign()
        handler = partial(QuietHandler, directory=str(self.web))
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.close_server)
        self.base = "http://127.0.0.1:" + str(self.server.server_port)
        self.output = self.root / "installed/zcard"
        self.output.parent.mkdir()
        self.output.write_bytes(b"existing-program")

    def close_server(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def sign(self):
        self.manifest["signature"] = ""
        content = json.dumps(self.manifest, ensure_ascii=False, separators=(",", ":"))
        for char in "<>&\u2028\u2029":
            content = content.replace(char, "\\u%04x" % ord(char))
        signed = self.root / "signed"
        signed.write_bytes(content.encode())
        signature = subprocess.check_output(["openssl", "pkeyutl", "-sign", "-inkey", str(self.private), "-rawin", "-in", str(signed)])
        self.manifest["signature"] = base64.b64encode(signature).decode()
        (self.files / "update.json").write_text(json.dumps(self.manifest, ensure_ascii=False))
        (self.files / "SHA256SUMS").write_text("".join(f"{f['sha256']}  {f['name']}\n" for f in self.manifest["files"]))

    def download(self, arch="arm64", version=None):
        release.download(version or self.tag, arch, self.output, self.base)

    def assert_rejected(self):
        with self.assertRaises(ValueError):
            self.download()
        self.assertEqual(self.output.read_bytes(), b"existing-program")

    def test_both_architectures_download_and_become_executable(self):
        for arch in ("amd64", "arm64"):
            self.download(arch)
            self.assertEqual(self.output.read_bytes(), (self.files / ("zcard-linux-" + arch)).read_bytes())
            self.assertEqual(self.output.stat().st_mode & 0o777, 0o755)

    def test_latest_pins_one_tag_and_legacy_aliases_resolve(self):
        (self.web / "latest.json").write_text(json.dumps({"tag_name": self.tag}))
        with patch.object(release, "LATEST_API", self.base + "/latest.json"):
            for version in ("latest", "auto", "dev"):
                self.download(version=version)
                self.assertEqual(self.output.read_bytes(), (self.files / "zcard-linux-arm64").read_bytes())

    def test_forged_manifest_rejected(self):
        raw = json.loads((self.files / "update.json").read_text())
        raw["notes"] = "forged"
        (self.files / "update.json").write_text(json.dumps(raw))
        self.assert_rejected()

    def test_corrupt_or_truncated_binary_rejected(self):
        original = (self.files / "zcard-linux-arm64").read_bytes()
        for data in (b"X" * len(original), original[:-1], original + b"overflow"):
            (self.files / "zcard-linux-arm64").write_bytes(data)
            self.assert_rejected()

    def test_mismatched_checksums_rejected(self):
        (self.files / "SHA256SUMS").write_text("0" * 64 + "  zcard-linux-arm64\n")
        self.assert_rejected()

    def test_wrong_signed_version_or_missing_architecture_rejected(self):
        self.manifest["version"] = "v1.2.59"
        self.sign()
        self.assert_rejected()
        self.manifest["version"] = self.tag
        self.manifest["files"] = self.manifest["files"][:1]
        self.sign()
        self.assert_rejected()

    def test_unsupported_architecture_and_invalid_tag_fail_before_download(self):
        with patch.object(release, "fetch", side_effect=AssertionError("unexpected download")):
            for arch, version in (("386", self.tag), ("arm64", "../bad")):
                with self.assertRaises(ValueError):
                    self.download(arch, version)

    def test_pinned_public_key_matches_application(self):
        source = (ROOT / "server/internal/platform/updater/updater.go").read_text()
        self.assertIn('DefaultPublicKeyHex = "' + self.public_key_from_script() + '"', source)

    @staticmethod
    def public_key_from_script():
        import re
        return re.search(r'PUBLIC_KEY = "([a-f0-9]+)"', (ROOT / "deploy/download-release.py").read_text())[1]


if __name__ == "__main__":
    unittest.main()
