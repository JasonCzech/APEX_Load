import unittest
from unittest.mock import AsyncMock, patch

import server


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
