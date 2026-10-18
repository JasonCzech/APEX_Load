import importlib.util
from pathlib import Path
import socket
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("local_brokers", Path(__file__).with_name("run-local-broker-integration.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class LocalBrokerRunnerTest(unittest.TestCase):
    def test_requires_an_actual_live_test_pass(self):
        for events, expected in [
            ('{"Action":"pass","Test":"live"}\n{"Action":"pass"}\n', True),
            ('{"Action":"skip","Test":"live"}\n{"Action":"pass"}\n', False),
            ('{"Action":"pass","Test":"live"}\n{"Action":"fail"}\n', False),
            ('compiler warning\n{"Action":"pass","Test":"other"}\n', False),
            ('[]\nnull\n', False),
        ]:
            with self.subTest(events=events), tempfile.TemporaryDirectory() as directory:
                log = Path(directory) / "run.jsonl"
                log.write_text(events)
                self.assertEqual(runner.live_test_passed(log, "live"), expected)

    def test_refuses_an_existing_broker_before_startup(self):
        with socket.socket() as listener:
            try:
                listener.bind(("127.0.0.1", 19092))
            except OSError:
                # An actual local broker is also correctly refused.
                pass
            else:
                listener.listen()
            with self.assertRaisesRegex(RuntimeError, "Port 19092 is in use"):
                runner.require_free_ports(["kafka"])

    def test_pins_brokers_and_uses_real_adapters(self):
        fixture = runner.COMPOSE.read_text()
        self.assertEqual(fixture.count("@sha256:"), 2)
        self.assertIn("kafka_real", runner.PROFILES["kafka"][0])
        self.assertIn("jms_solace_real", runner.PROFILES["solace"][0])

    def test_kafka_admin_listener_is_separate_from_host_clients(self):
        service = runner.kafka_fixture_override()["services"]["kafka"]
        self.assertNotIn("ports", service)
        advertised = service["environment"]["KAFKA_ADVERTISED_LISTENERS"]
        self.assertIn("PLAINTEXT://127.0.0.1:19092", advertised)
        self.assertIn("INTERNAL://127.0.0.1:9094", advertised)


if __name__ == "__main__":
    unittest.main()
