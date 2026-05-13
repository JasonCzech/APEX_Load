import importlib.util
from pathlib import Path
import unittest
from unittest.mock import AsyncMock, patch


def _load_server_module():
    server_path = Path(__file__).with_name("server.py")
    spec = importlib.util.spec_from_file_location("stormrunner_mcp_server", server_path)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load mcp server module for testing")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


server = _load_server_module()


class ContinueTestAliasTests(unittest.IsolatedAsyncioTestCase):
    async def test_continue_test_delegates_to_resume_test(self) -> None:
        with patch.object(server, "resume_test", AsyncMock(return_value='{"status":"resumed"}')) as resume_mock:
            result = await server.continue_test("test-123")

        resume_mock.assert_awaited_once_with("test-123")
        self.assertEqual(result, '{"status":"resumed"}')

    async def test_continue_test_propagates_resume_errors(self) -> None:
        with patch.object(server, "resume_test", AsyncMock(side_effect=RuntimeError("resume failed"))) as resume_mock:
            with self.assertRaisesRegex(RuntimeError, "resume failed"):
                await server.continue_test("test-123")

        resume_mock.assert_awaited_once_with("test-123")


if __name__ == "__main__":
    unittest.main()
