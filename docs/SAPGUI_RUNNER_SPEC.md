# SAP GUI Runner Bridge Spec

## Status

This is a runner specification, not an implemented runtime. SAP GUI imports can produce a `sap_gui` DSL draft, but launch remains blocked with `external_runner_required` until a Windows runner bridge is deployed and entitled.

## Transport

SAP GUI execution uses the existing controller-to-generator gRPC control plane with runner registration extensions:

- `runner_class`: `windows_sap_gui_runner`
- `protocols`: `sap_gui`
- `os`: `windows`
- `sap_gui_client_version`
- `sap_gui_scripting_enabled`
- `desktop_session_capacity`

The controller schedules only `sap_gui` groups to generators advertising this runner class and a positive desktop session capacity.

## Execution Model

The runner implements the standard protocol lifecycle on Windows and drives SAP GUI through SAP GUI Scripting COM:

- `FindById` resolves DSL `target` control IDs such as `wnd[0]/usr/txtRSYST-BNAME`.
- `.Text`, `.Key`, `.Selected`, `.Press()`, and table/grid/tree methods perform typed DSL actions.
- `Session.Busy`, status bar changes, screen changes, and bounded polling implement `sapgui_sync`.
- Status bar text and control values feed assertions and protocol evidence.

Each VUser owns an isolated desktop session or an explicitly governed session slot. The runner must not multiplex unrelated tests into the same SAP GUI session.

## Capacity

Linux cgroup pod metrics do not describe Windows desktop session capacity. The Windows runner reports `desktop_session_capacity`, current active sessions, and SAP GUI client readiness. The default planning envelope is 25 VUsers per runner until customer-calibrated evidence supersedes it.

## Placement And Governance

Launch requires:

- `sap_gui_foundation` entitlement.
- `windows_sap_gui_runner` placement class.
- SAP GUI client installed and version reported.
- SAP GUI Scripting enabled on the client and server policy.
- Secret provisioning for `config.sapgui.password`.

Without these signals, preflight returns an `external_runner_required` conflict and direct launch is blocked.

## Evidence To Graduate

The SAP GUI profile can move from external-runner-required to executable only after:

- Representative import fixtures validate the typed DSL mapping.
- Runner smoke tests cover logon, set, press, grid/table/tree, sync, verify, and error handling.
- Capacity calibration publishes desktop session density evidence.
- Governance verifies entitlement, placement, and secret isolation.
