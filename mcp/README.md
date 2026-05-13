# Stormrunner MCP Server

This directory contains a Model Context Protocol (MCP) server that allows AI agents to interface directly with the Stormrunner Performance Testing Platform.

## Features

Provides the following tools to an AI agent:
- `list_tests`: List all scenarios and their status.
- `get_test_details`: Retrieve live metrics and detailed status for a test.
- `start_test` / `stop_test`: Lifecycle controls for tests.
- `pause_test` / `resume_test` / `continue_test`: Robust execution controls.
- `list_scripts`: Browse the script library.
- `get_system_health`: Monitor controller health.

## Installation

1. Ensure you have Python 3.10+ installed.
2. Install dependencies:
   ```bash
   pip install -r requirements.txt
   ```

## Configuration

The server connects to the Stormrunner API at `http://localhost:8080/api/v1` by default. You can override this using the `STORMRUNNER_URL` environment variable.

### Usage with Claude Desktop

Add the following to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "stormrunner": {
      "command": "python",
      "args": ["/path/to/Project_Stormrunner/mcp/server.py"],
      "env": {
        "STORMRUNNER_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

## Running Manually

To test the server manually:
```bash
python server.py
```
