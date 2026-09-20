#!/usr/bin/env python3
"""Remove ephemeral pairing/secret values from retained recording evidence."""
import base64
import json
from pathlib import Path
import sys
import zipfile


def redact_text(text, values):
    for value in values:
        text = text.replace(value, '[redacted]')
        text = text.replace(json.dumps(value)[1:-1], '[redacted]')
    # The Playwright JSON reporter embeds body attachments as base64.
    try:
        document = json.loads(text)
    except (ValueError, RecursionError):
        # Playwright .trace/.network resources are JSON lines, not one JSON
        # document. Inspect each event so login input and request bodies are
        # protected even when their values are deliberately ordinary words.
        lines = text.splitlines(keepends=True)
        if len(lines) > 1:
            return ''.join(redact_text(line.rstrip('\r\n'), values) +
                           ('\n' if line.endswith('\n') else '') for line in lines)
        return text

    def visit(node):
        if isinstance(node, dict):
            for key in ('password', 'pairing_token'):
                if isinstance(node.get(key), str):
                    node[key] = '[redacted]'
            params = node.get('params')
            if isinstance(params, dict) and 'password' in str(params.get('selector', '')).lower():
                if isinstance(params.get('value'), str):
                    params['value'] = '[redacted]'
            # Reporter attachments use body; HAR response content uses text
            # with an explicit base64 encoding. Both can contain nested JSON.
            encoded_fields = ['body']
            if node.get('encoding') == 'base64':
                encoded_fields.append('text')
            for field in encoded_fields:
                if not isinstance(node.get(field), str):
                    continue
                try:
                    decoded = base64.b64decode(node[field], validate=True).decode()
                    replacement = redact_text(decoded, values)
                    if replacement != decoded:
                        node[field] = base64.b64encode(replacement.encode()).decode()
                except (ValueError, UnicodeError):
                    pass
            for key, child in node.items():
                # Network traces embed JSON postData as an escaped string.
                if isinstance(child, str) and child.lstrip().startswith(('{', '[')):
                    node[key] = redact_text(child, values)
                    continue
                visit(child)
        elif isinstance(node, list):
            for child in node:
                visit(child)
    visit(document)
    return json.dumps(document, ensure_ascii=False)


def redact_bytes(data, values):
    try:
        return redact_text(data.decode('utf-8'), values).encode('utf-8')
    except UnicodeError:
        return data


def sanitize(root, values):
    for path in Path(root).rglob('*'):
        if not path.is_file():
            continue
        if path.suffix == '.zip':
            temporary = path.with_suffix('.redacted.zip')
            with zipfile.ZipFile(path) as source, zipfile.ZipFile(temporary, 'w') as target:
                for member in source.infolist():
                    target.writestr(member, redact_bytes(source.read(member), values))
            temporary.replace(path)
        else:
            # Download attachments have content-addressed, extensionless names.
            # UTF-8 decoding leaves binary images/videos untouched.
            path.write_bytes(redact_bytes(path.read_bytes(), values))


if __name__ == '__main__':
    registry = Path(sys.argv[2])
    values = [json.loads(line) for line in registry.read_text().splitlines()] if registry.exists() else []
    sanitize(sys.argv[1], sorted(set(values), key=len, reverse=True))
