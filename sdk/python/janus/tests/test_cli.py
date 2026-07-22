import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parents[1]))

from janus_cli import asset_name, target_for


class CliTests(unittest.TestCase):
    def test_supported_targets(self):
        self.assertEqual(target_for("darwin", "arm64"), "darwin-arm64")
        self.assertEqual(target_for("darwin", "x86_64"), "darwin-amd64")
        self.assertEqual(target_for("linux", "aarch64"), "linux-arm64")
        self.assertEqual(target_for("linux", "x86_64"), "linux-amd64")
        self.assertEqual(target_for("windows", "AMD64"), "windows-amd64")
        self.assertEqual(asset_name("windows-amd64"), "janus-windows-amd64.exe")

    def test_unsupported_targets(self):
        with self.assertRaises(RuntimeError):
            target_for("freebsd", "x86_64")
        with self.assertRaises(RuntimeError):
            target_for("windows", "arm64")


if __name__ == "__main__":
    unittest.main()
