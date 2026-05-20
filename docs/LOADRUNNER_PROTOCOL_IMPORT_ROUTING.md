# LoadRunner Protocol Import Routing

APEX Load's LoadRunner/VuGen ZIP importer is protocol-aware. It classifies the archive before conversion, using `.usr` `[General] Type` as the primary signal and source signatures as validation or fallback evidence.

## Import Modes

| Import mode | Meaning |
| --- | --- |
| `executable` | APEX Load has a native converter/runtime path and returns a generated script. |
| `review_only` | APEX Load can classify and preserve the source, but promotion requires operator review or a different importer. |
| `external_runner_required` | The source protocol needs a non-APEX Load runner bridge before execution. |
| `unsupported` | The protocol is recognized or suspected, but no current conversion path is available. |

## Current Routing

| LoadRunner/VuGen family | APEX Load route |
| --- | --- |
| Web HTTP/HTML | `http` executable import |
| Web Services / SOAP | `soap` executable import when SOAP evidence is exclusive; mixed SOAP/HTTP stays HTTP with warning |
| TruClient Mobile Web | `browser` executable import with browser replay gate |
| TruClient 2.0 Web | `browser` review path with required replay and selector evidence |
| Database / ODBC SQL-style scripts | `database` executable import for SQL statements, binds, transactions, and DSN/runtime hints |
| DNS, FTP, SMTP, POP3, IMAP, LDAP, Winsock | `network` executable import with subprotocol config and endpoint placeholders |
| Oracle-Web / Siebel-Web | `http` executable import plus Oracle E-Business correlation review pack |
| SAP GUI, Oracle NCA, Oracle 2-Tier | External-runner review artifact |
| MAPI, Tuxedo, Template VUsers | Review artifact until a runner/converter is approved |

If `.usr` metadata and source content conflict, APEX Load returns a review artifact instead of pretending the import is executable. The harmless exception is Web HTTP/HTML metadata with SOAP-only request evidence, which routes to SOAP.

## API Surface

`POST /api/v1/scripts/import/loadrunner` and `POST /api/v1/scripts/import/loadrunner/validate` now include:

- `detected_protocol`: canonical LoadRunner protocol, APEX Load route, bundle, import mode, confidence, signals, conflicts, warnings, and required evidence.
- `review_artifact`: present for review-only, external-runner, unsupported, and review-gated imports.
- `summary.detected_protocol`, `summary.review_artifact`, and `summary.import_mode`.

Executable imports still return `script`. Non-executable review artifacts return `script: null` and `scripts: []`.

The dashboard LoadRunner import page shows protocol coverage cards, detected protocol details, gating evidence, and a `Save Review Artifact` action for non-executable imports.
