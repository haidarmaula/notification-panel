"""Diagnostic retention and process-status checks for the optional test wrapper."""

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("quiet-go-test.py")
spec = importlib.util.spec_from_file_location("quiet_go_test", SCRIPT)
quiet = importlib.util.module_from_spec(spec)
spec.loader.exec_module(quiet)


def stream(*events):
    return "".join(json.dumps(event) + "\n" for event in events)


class QuietGoTest(unittest.TestCase):
    def test_failure_retains_entire_package_output_and_skip_reason(self):
        events = [
            {"Action": "output", "Package": "bad", "Output": "setup detail\n"},
            {"Action": "output", "Package": "bad", "Test": "TestBad", "Output": "assertion failed\n"},
            {"Action": "fail", "Package": "bad", "Test": "TestBad"},
            {"Action": "fail", "Package": "bad"},
            {"Action": "output", "Package": "good", "Test": "TestSkip", "Output": "needs Docker\n"},
            {"Action": "skip", "Package": "good", "Test": "TestSkip"},
            {"Action": "pass", "Package": "good"},
        ]
        result = quiet.summarize(stream(*events), 1)
        for diagnostic in ("setup detail", "assertion failed", "needs Docker", "FAIL bad"):
            self.assertIn(diagnostic, result)

    def test_non_json_compile_error_is_shown_in_full(self):
        raw = "# hello/broken\n./main.go:3: undefined: missing\nFAIL\n"
        self.assertIn(raw, quiet.summarize(raw, 1))

    def test_new_or_incomplete_protocol_falls_back_without_loss(self):
        for raw in (stream({"Action": "new-event", "Package": "p"}),
                    stream({"Action": "run", "Package": "p", "Test": "TestHung"}),
                    stream({"Action": "output", "Package": "p", "Output": 123})):
            with self.subTest(raw=raw):
                self.assertIn(raw, quiet.summarize(raw, 1))

    def test_exit_disagreement_cannot_hide_failure(self):
        raw = stream({"Action": "pass", "Package": "p"})
        self.assertIn(raw, quiet.summarize(raw, 2))

    def test_wrapper_preserves_nonstandard_exit_and_raw_log(self):
        with tempfile.TemporaryDirectory() as directory:
            fake_go = Path(directory) / "go"
            fake_go.write_text(f"#!{sys.executable}\nimport sys\n"
                               "print('compiler diagnostic with full details')\nsys.exit(37)\n")
            fake_go.chmod(0o700)
            result = subprocess.run([sys.executable, str(SCRIPT), "--go", str(fake_go), "--", "./..."],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 37, result.stderr)
            self.assertIn("compiler diagnostic with full details", result.stdout)
            log = Path(result.stdout.split("Full log: ", 1)[1].strip())
            self.assertEqual(log.read_text(), "compiler diagnostic with full details\n")
            self.assertEqual(json.loads(log.with_name("metrics.json").read_text())["returncode"], 37)

    @unittest.skipIf(sys.platform == "win32", "POSIX signal exit convention")
    def test_signal_is_reported_as_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            fake_go = Path(directory) / "go"
            fake_go.write_text(f"#!{sys.executable}\nimport os, signal\n"
                               "os.kill(os.getpid(), signal.SIGTERM)\n")
            fake_go.chmod(0o700)
            result = subprocess.run([sys.executable, str(SCRIPT), "--go", str(fake_go)],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 143, result.stderr)
            self.assertIn("Unrecognized/incomplete", result.stdout)


if __name__ == "__main__":
    unittest.main()
