#!/usr/bin/env python3
"""Real TLS JMX acceptance; all processes, keys, and certificates are disposable."""
import json
import os
import hashlib
import pathlib
import select
import socket
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent

class JavaMonitorAcceptance(unittest.TestCase):
    def test_tls_jmx_fixed_attributes_and_authentication(self):
        with tempfile.TemporaryDirectory(prefix="apex-jmx-") as directory:
            subprocess.run(["javac", "-d", directory, str(ROOT / "ApexMonitor.java"), str(ROOT / "testdata/JMXFixture.java")], check=True, capture_output=True)
            keystore = str(pathlib.Path(directory) / "identity.p12")
            subprocess.run(["keytool", "-genkeypair", "-alias", "fixture", "-keyalg", "RSA", "-storetype", "PKCS12", "-keystore", keystore, "-storepass", "changeit", "-keypass", "changeit", "-dname", "CN=localhost", "-ext", "SAN=ip:127.0.0.1", "-validity", "1"], check=True, capture_output=True)
            with socket.socket() as reserved:
                reserved.bind(("127.0.0.1", 0))
                port = reserved.getsockname()[1]
            fixture = subprocess.Popen(["java", "-Djavax.net.ssl.keyStore=" + keystore, "-Djavax.net.ssl.keyStorePassword=changeit", "-cp", directory, "JMXFixture", str(port)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                self.assertTrue(select.select([fixture.stdout], [], [], 15)[0], "fixture readiness timed out")
                self.assertEqual(fixture.stdout.readline().strip(), "READY")
                command = ["java", "-Djavax.net.ssl.trustStore=" + keystore, "-Djavax.net.ssl.trustStorePassword=changeit", "-cp", directory, "ApexMonitor", "jmx", f"jmx://127.0.0.1:{port}", "jvm-basic-v1"]
                result = subprocess.run(command, input="monitor\nfixture-secret\n", text=True, capture_output=True, timeout=15)
                self.assertEqual(result.returncode, 0, result.stderr)
                points = json.loads(result.stdout)
                self.assertEqual({p["metric_name"] for p in points}, {"jvm.threads", "jvm.uptime", "jvm.heap.used"})
                self.assertTrue(all(p["value"] >= 0 for p in points))
                denied = subprocess.run(command, input="monitor\nwrong\n", text=True, capture_output=True, timeout=15)
                self.assertNotEqual(denied.returncode, 0)
                untrusted = subprocess.run([part for part in command if not part.startswith("-Djavax.net.ssl.trustStore")], input="monitor\nfixture-secret\n", text=True, capture_output=True, timeout=15)
                self.assertNotEqual(untrusted.returncode, 0)
            finally:
                fixture.communicate("stop\n", timeout=10)

    @unittest.skipUnless(os.environ.get("APEX_MONITOR_POSTGRES_BIN") and os.environ.get("APEX_MONITOR_JDBC_JAR"), "set disposable PostgreSQL bin and pinned JDBC jar paths")
    def test_jdbc_read_only_health_and_tls(self):
        binary = pathlib.Path(os.environ["APEX_MONITOR_POSTGRES_BIN"])
        jar = pathlib.Path(os.environ["APEX_MONITOR_JDBC_JAR"])
        self.assertEqual(hashlib.sha256(jar.read_bytes()).hexdigest(), "6e0e4cc2d8cae902084f8a2b18728b073a6fd9d1f87c9d8bff8f298c18185b93")
        with tempfile.TemporaryDirectory(prefix="apex-jdbc-") as directory:
            root = pathlib.Path(directory)
            password = root / "password"
            password.write_text("fixture-secret\n"); password.chmod(0o600)
            data = root / "data"
            subprocess.run([str(binary / "initdb"), "-D", str(data), "-U", "monitor", "--auth-local=trust", "--auth-host=scram-sha-256", "--pwfile=" + str(password)], check=True, capture_output=True)
            certificate, key = root / "server.crt", root / "server.key"
            configuration = root / "openssl.cnf"
            configuration.write_text("[req]\ndistinguished_name=dn\nx509_extensions=extensions\nprompt=no\n[dn]\nCN=localhost\n[extensions]\nsubjectAltName=IP:127.0.0.1\n")
            subprocess.run(["openssl", "req", "-new", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-config", str(configuration), "-keyout", str(key), "-out", str(certificate)], check=True, capture_output=True)
            key.chmod(0o600)
            with (data / "postgresql.conf").open("a") as config:
                config.write(f"\nssl=on\nssl_cert_file='{certificate}'\nssl_key_file='{key}'\n")
            with socket.socket() as reserved:
                reserved.bind(("127.0.0.1", 0)); port = reserved.getsockname()[1]
            subprocess.run([str(binary / "pg_ctl"), "-D", str(data), "-l", str(root / "postgres.log"), "-w", "-t", "15", "-o", f"-h 127.0.0.1 -p {port} -k {directory}", "start"], check=True, capture_output=True)
            try:
                subprocess.run(["javac", "-d", directory, str(ROOT / "ApexMonitor.java")], check=True, capture_output=True)
                command = ["java", "-cp", directory + os.pathsep + str(jar), "ApexMonitor", "jdbc", f"postgresql://127.0.0.1:{port}/postgres", "connection-health-v1", str(certificate)]
                result = subprocess.run(command, input="monitor\nfixture-secret\n", text=True, capture_output=True, timeout=15)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)[0]["metric_name"], "jdbc.connection_health.duration")
                denied = subprocess.run(command, input="monitor\nwrong\n", text=True, capture_output=True, timeout=15)
                self.assertNotEqual(denied.returncode, 0)
                injected = command.copy(); injected[-2] = "SELECT pg_sleep(20)"
                refused = subprocess.run(injected, input="monitor\nfixture-secret\n", text=True, capture_output=True, timeout=15)
                self.assertNotEqual(refused.returncode, 0)
            finally:
                subprocess.run([str(binary / "pg_ctl"), "-D", str(data), "-m", "immediate", "-w", "stop"], check=True, capture_output=True)

if __name__ == "__main__":
    unittest.main()
