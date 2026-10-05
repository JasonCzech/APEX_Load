import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("native_startup", Path(__file__).with_name("test_native_worker_startup.py"))
startup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(startup)

class NativeStartupGateTest(unittest.TestCase):
    def test_exact_handled_exit_and_no_side_effects(self):
        with tempfile.TemporaryDirectory() as temp:
            binary = Path(temp) / "fixture"
            binary.write_bytes(b"fixture")
            calls = []
            def run(command, **kwargs):
                calls.append((command, kwargs))
                return subprocess.CompletedProcess(command, 78)
            report = startup.verify_binary(binary, run)
            self.assertEqual(len(calls), 4)
            self.assertEqual(len(report["cases"]), 4)
            self.assertTrue(all(c[1]["timeout"] == 5 for c in calls))
            self.assertTrue(all(c[1]["stdin"] == subprocess.DEVNULL for c in calls))
    def test_ordinary_startup_exit_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            binary = Path(temp) / "fixture"
            binary.write_bytes(b"fixture")
            with self.assertRaises(RuntimeError):
                startup.verify_binary(binary, lambda command, **kwargs: subprocess.CompletedProcess(command, 1))
    def test_side_effect_even_with_handled_exit_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            binary = Path(temp) / "fixture"
            binary.write_bytes(b"fixture")
            def run(command, **kwargs):
                (kwargs["cwd"] / "forbidden.db").write_bytes(b"created")
                return subprocess.CompletedProcess(command, 78)
            with self.assertRaises(RuntimeError): startup.verify_binary(binary, run)
    def test_fatal_output_is_rejected_even_when_exit_races_to_78(self):
        for output in [dict(stderr=b"runtime: kevent on fd 3 failed\nfatal error: netpoll\n"), dict(stdout=b"unexpected listener startup")]:
            with self.subTest(output=output), tempfile.TemporaryDirectory() as temp:
                binary = Path(temp) / "fixture"
                binary.write_bytes(b"fixture")
                def run(command, **kwargs):
                    return subprocess.CompletedProcess(command, 78, **output)
                with self.assertRaises(startup.NativeStartupFailure) as caught:
                    startup.verify_binary(binary, run)
                report = caught.exception.report
                self.assertFalse(report["passed"])
                self.assertFalse(report["cases"][-1]["silent"])
                self.assertTrue(report["cases"][-1]["filesystem_unchanged"])
                self.assertEqual(report["cases"][-1]["exit_code"], 78)

    def test_desktop_selection_rejects_multiple_candidates(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "apexload").write_bytes(b"fixture")
            (root / "apexload.exe").write_bytes(b"fixture")
            with self.assertRaises(RuntimeError): startup.desktop_binary(root)

if __name__ == "__main__": unittest.main()
