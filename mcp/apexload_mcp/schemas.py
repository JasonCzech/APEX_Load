"""Generated schema descriptions without changing curated tool execution."""
from functools import lru_cache
import json
from pathlib import Path
from typing import Annotated, Any

from pydantic import WithJsonSchema


@lru_cache(maxsize=1)
def _input_schemas() -> dict:
    return json.loads(Path(__file__).with_name("input_schemas.json").read_text())["tools"]


def describe_endpoint_inputs(name: str, handler) -> None:
    # Metadata enriches the MCP schema. Keep the established flexible Python
    # argument types and HTTP guards, including missing paths and If-Match.
    for parameter, schema in _input_schemas().get(name, {}).items():
        value_type = dict[str, Any] | list[Any] | None if parameter == "payload" else dict[str, Any] | None
        handler.__annotations__[parameter] = Annotated[value_type, WithJsonSchema({"anyOf": [schema, {"type": "null"}]})]
