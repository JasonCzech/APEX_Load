# Recording and replay usability audit

Fresh screenshots were saved and inspected from a disposable local controller built at `903c6690`. The audit used a blank browser draft and setup forms, with no customer upload, recording, saved script or replay. It evaluates visible onboarding and navigation. It does not validate live recording/replay, keyboard or screen-reader operation, correlation repair, physical Chrome/iPad behavior, or current main's later navigation changes. The related [roadmap](roadmap.md) separates these evidence limits.

## Captured flow

### Step 1 — Enter authoring

**General health: good foundation.** Create, Record and Import are distinct entry choices. The template/editor/action-block table makes supported choices discoverable. Dense choices could benefit from a task-oriented recommendation once a user selects physical browser, HTTP load or source import.

Step 1: authoring entry choices (`screenshots/01-authoring-entry.jpg`)

### Step 2 — Choose an iPad recording profile

**General health: useful desktop setup; incomplete for the requested device.** The selected `ipad_air` profile exposes viewport/device configuration, and the form says it launches controlled Chromium on this machine. There is no physical-device or installed Chrome-app selector in this view. Make “Emulated iPad on this workstation” explicit and provide a separate physical Chrome target with readiness and observed identity. The preset name alone could suggest a broader guarantee than the accompanying copy.

Step 2: iPad recording preset (`screenshots/02-ipad-recording-preset.jpg`)

### Step 3 — Inspect proxy onboarding

**General health: useful desktop guidance; target-specific guidance missing.** Expanded instructions cover macOS/Windows proxy setup and the recorder CA. Add physical iPad Chrome guidance for Mac inspection, inspectability checks and manual HAR export, followed by any separately qualified device-proxy route. Show reachable proxy requirements and explicit trust cleanup where that route is chosen. No proxy settings, certificate trust or device configuration were changed in this audit.

Step 3: proxy onboarding (`screenshots/03-proxy-onboarding.jpg`)

### Step 4 — Open browser debugging

**General health: strong existing workspace; live behavior unverified.** Author/Debug navigation, stepping, failure breaks, variable watches, breakpoint controls, action outline and Source/Runtime/Browser Replay tabs are visible. The blank draft is idle, so this is navigation evidence. Extend the same workspace with interaction-to-Aura joins, recorded/replay diffs, authoritative pause-state values and reviewed repairs. Basic watches and the debugger already exist.

Step 4: browser replay workspace (`screenshots/04-browser-replay-workspace.jpg`)

## Design priorities

| Priority | Finding | Recommended flow |
| --- | --- | --- |
| High | Emulated device and actual browser identity need clearer separation. | Choose physical Chrome/iPad → readiness checks → show observed device/app → record. Retain desktop emulation as an explicit option. |
| High | Desktop proxy onboarding does not answer physical Chrome setup. | Route instructions by chosen target and capture method, with concrete prerequisite/error resolution and capture capability limits. |
| High | The requested business correlation spans multiple existing tools. | Review completeness → per-subaction Aura outcome → producer/consumer proof → preview repair → replay result in the existing workspace. |
| Medium | Dense authoring choices require the user to infer the route. | Offer a recommendation based on desired output, while leaving detailed choices available. |
| Medium | Runtime labels should explain what was actually applied. | Compare requested settings with observed engine/device capabilities and preserve unresolved source settings in the review. |

## Visible accessibility risks and limits

The dense authoring table and debug workspace need keyboard navigation and accessible relationship checks across the action outline, tabs, evidence and repair controls. Preserve textual readiness/blocker/status messages so color does not carry the result alone. Screenshots cannot establish contrast ratios, focus order, screen-reader announcements or touch target dimensions; these are verification requirements, not confirmed violations. Physical iPad testing must include keyboard overlays, zoom/orientation and reachable touch controls.

The desired user journey is: **choose actual target → verify readiness → record → review capture completeness → repair correlation → replay one business operation → save linked browser/HTTP outputs**. Reuse the existing editor/debugger and shared recording receipts so the user does not have to reconstruct the journey across disconnected views.
