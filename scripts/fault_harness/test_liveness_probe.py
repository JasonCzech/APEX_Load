import math
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from liveness_probe import main, measure, probe, validate


class Clock:
    def __init__(self):
        self.now = 0
    def __call__(self):
        return self.now
    def sleep(self, seconds):
        self.now += seconds


class LivenessTest(unittest.TestCase):
    def run_window(self, read=None, clock=None, seconds=10):
        clock = clock or Clock()
        return measure([18001, 18002], seconds,
                       read=read or (lambda ports: [{"ok": True, "seconds": .01} for _ in ports]),
                       clock=clock, sleep=clock.sleep)

    def test_complete_window_and_per_controller_nearest_rank(self):
        report, samples = self.run_window(seconds=600)
        self.assertTrue(report['passed'])
        self.assertEqual(len(samples), 601)
        self.assertEqual(samples[-1]['elapsed_seconds'], 600)
        self.assertEqual([c['p99_ms'] for c in report['controllers']], [10, 10])

    def test_invalid_configuration(self):
        for ports in ([], [True], [0], [65536], [80, 80], list(range(1, 10))):
            with self.subTest(ports=ports), self.assertRaises(ValueError):
                validate(ports, 600)
        for duration in (True, 0, 9, 1801, math.nan, math.inf, '600'):
            with self.subTest(duration=duration), self.assertRaises(ValueError):
                validate([80], duration)

    def test_failed_or_slow_endpoint_cannot_be_hidden_by_other_controller(self):
        for row in ({'ok': False, 'seconds': .01}, {'ok': True, 'seconds': .5}):
            with self.subTest(row=row):
                report, _ = self.run_window(read=lambda ports: [row, {'ok': True, 'seconds': .01}])
                self.assertFalse(report['passed'])
                self.assertEqual(report['controllers'][1]['failures'], 0)

    def test_percentile_uses_the_tail_instead_of_mean_latency(self):
        calls = 0
        def read(ports):
            nonlocal calls
            calls += 1
            return [{'ok': True, 'seconds': .6 if calls == 1 else .001},
                    {'ok': True, 'seconds': .001}]
        report, _ = self.run_window(read=read)
        self.assertFalse(report['passed'])
        self.assertEqual(report['controllers'][0]['p99_ms'], 600)

    def test_reject_invalid_probe_numbers_and_shapes_without_disclosing_payload(self):
        for row in ({'ok': True, 'seconds': math.nan}, {'ok': True, 'seconds': -1},
                    {'ok': 1, 'seconds': .1}, {'ok': True, 'seconds': True}):
            report, samples = self.run_window(read=lambda ports: [row, row])
            self.assertFalse(report['passed'])
            self.assertEqual(report['reason'], 'probe_failed')
            self.assertEqual(samples, [])
        report, _ = self.run_window(read=lambda ports: [])
        self.assertFalse(report['passed'])

    def test_interrupt_and_adapter_exception_retain_completed_samples(self):
        for error in (KeyboardInterrupt(), RuntimeError('private response')):
            count = 0
            def read(ports):
                nonlocal count
                count += 1
                if count == 3:
                    raise error
                return [{'ok': True, 'seconds': .01, 'secret': 'private'} for _ in ports]
            report, samples = self.run_window(read=read)
            self.assertFalse(report['passed'])
            self.assertEqual(len(samples), 2)
            self.assertNotIn('private', str(report) + str(samples))

    def test_sampling_gap_and_final_deadline_cannot_pass(self):
        for delay in (4, 20):
            clock = Clock()
            def read(ports):
                clock.now += delay
                return [{'ok': True, 'seconds': .01} for _ in ports]
            report, _ = self.run_window(read=read, clock=clock)
            self.assertFalse(report['passed'])

    def test_transport_is_bounded_loopback_and_does_not_follow_redirects(self):
        for response, expected in ((subprocess.CompletedProcess([], 0, b'200'), True),
                                   (subprocess.CompletedProcess([], 0, b'302'), False),
                                   (subprocess.CompletedProcess([], 28, b'200'), False)):
            with patch('liveness_probe.subprocess.run', return_value=response) as run:
                self.assertEqual(probe(18001)['ok'], expected)
                args = run.call_args.args[0]
                self.assertEqual(args[:2], ['curl', '--disable'])
                self.assertEqual(args[-1], 'http://127.0.0.1:18001/api/v1/system/health')
                self.assertIn('--max-time', args)
                self.assertNotIn('--location', args)
                self.assertEqual(run.call_args.kwargs['timeout'], 2.5)
        with patch('liveness_probe.subprocess.run', side_effect=subprocess.TimeoutExpired('curl', 2.5)):
            self.assertFalse(probe(18001)['ok'])

    def test_cli_private_output_refuses_overwrite_and_failed_gate_exits_nonzero(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / 'evidence'
            argv = ['liveness_probe', '--ports', '18001', '--output', str(output)]
            report = {'passed': False}
            with patch('sys.argv', argv), patch('liveness_probe.signal.signal'), \
                    patch('liveness_probe.measure', return_value=(report, [])) as run:
                self.assertEqual(main(), 1)
                run.assert_called_once_with([18001], 600)
                self.assertEqual(json.loads((output / 'report.json').read_text()), report)
                self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o700)
                for path in output.iterdir():
                    self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
                self.assertEqual(main(), 1)
                self.assertEqual(run.call_count, 1)

    def test_slow_final_sample_fails(self):
        clock = Clock()
        def late(ports):
            if clock.now >= 10:
                clock.now += 5
            return [{'ok': True, 'seconds': .01} for _ in ports]
        report, _ = self.run_window(read=late, clock=clock)
        self.assertFalse(report['passed'])
        self.assertEqual(report['reason'], 'window_deadline_exceeded')

    def test_verdict_is_not_written_if_sample_persistence_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / 'evidence'
            argv = ['liveness_probe', '--ports', '18001', '--output', str(output)]
            real_open = os.open
            def fail_samples(path, *args):
                if Path(path).name == 'samples.json':
                    raise OSError('disk full')
                return real_open(path, *args)
            with patch('sys.argv', argv), patch('liveness_probe.signal.signal'), \
                    patch('liveness_probe.measure', return_value=({'passed': True}, [])), \
                    patch('liveness_probe.os.open', side_effect=fail_samples):
                self.assertEqual(main(), 1)
            self.assertFalse((output / 'report.json').exists())


if __name__ == '__main__':
    unittest.main()
