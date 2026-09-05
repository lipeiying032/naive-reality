import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import tomllib
import unittest
from urllib.parse import unquote, urlsplit


SCRIPT = Path(__file__).resolve().parents[1] / "scripts/migrate-h3-config.py"


class MigrationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.server = self.root / "old.toml"
        self.client = self.root / "old.json"
        self.server.write_text('mode="reality"\nlisten="127.0.0.1:8443"\n[reality]\nprivate_key="never-copy-this-secret"\ndest="third-party.test:443"\n[upstream]\naddr="127.0.0.1:18080"\n')
        self.client.write_text(json.dumps({
            "listen": "socks://127.0.0.1:1088",
            "proxy": "quic://user:p%3Aa%24ss@third-party.test:443",
            "reality": {"public_key": "old-key"},
            "reality-server-name": "third-party.test", "quic": {},
            "host-resolver-rules": "MAP third-party.test 127.0.0.1", "no-post-quantum": True,
        }))

    def run_migration(self, *extra):
        return subprocess.run([
            sys.executable, str(SCRIPT), "--server", str(self.server),
            "--client", str(self.client), "--hostname", "owned.example",
            "--cert", str(self.root / "cert.pem"), "--key", str(self.root / "key.pem"),
            "--web-root", str(self.root / "site"), "--out-dir", str(self.root / "new"), *extra,
        ], capture_output=True, text=True, timeout=10)

    def test_migrates_without_overwriting_or_copying_legacy_identity(self):
        before = (self.server.read_bytes(), self.client.read_bytes())
        result = self.run_migration()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(before, (self.server.read_bytes(), self.client.read_bytes()))
        output = self.root / "new"
        server = tomllib.loads((output / "h3frontend.toml").read_text())
        client = json.loads((output / "config.json").read_text())
        self.assertEqual(server["mode"], "origin")
        self.assertEqual(server["listen"], "127.0.0.1:8443")
        self.assertEqual(server["origin"]["password"], "p:a$ss")
        self.assertNotIn("reality", server)
        proxy = urlsplit(client["proxy"])
        self.assertEqual((proxy.hostname, proxy.port), ("owned.example", 443))
        self.assertEqual(unquote(proxy.password), server["origin"]["password"])
        self.assertEqual(set(client), {"listen", "proxy"})
        for path in output.iterdir():
            self.assertNotIn("never-copy-this-secret", path.read_text())
            if os.name == "posix":
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        before_output = (output / "config.json").read_bytes()
        self.assertNotEqual(self.run_migration().returncode, 0)
        self.assertEqual((output / "config.json").read_bytes(), before_output)

    def test_requires_owned_hostname(self):
        result = self.run_migration("--hostname", "https://not-a-host/")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "new").exists())

    def test_refuses_missing_proxy_credentials(self):
        self.client.write_text('{"proxy":"quic://third-party.test"}')
        self.assertNotEqual(self.run_migration().returncode, 0)
        self.assertFalse((self.root / "new").exists())

    def test_parse_errors_do_not_echo_secrets(self):
        self.server.write_text('mode = "never-copy-this-secret\n')
        result = self.run_migration()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("never-copy-this-secret", result.stderr)
        self.assertFalse((self.root / "new").exists())


if __name__ == "__main__":
    unittest.main()
