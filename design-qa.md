# Stepped Ramp Guided Worksheet — Design QA

- Source visual truth: `/Users/jasonczech/.codex/generated_images/01a05eb5-16da-7340-990d-d53e9fa49ed7/exec-0517e758-2b13-41ff-895b-3bcf0a8af76d.png`
- Implementation: `http://127.0.0.1:4000/#/tests?view=builder`
- Implementation screenshot evidence: `/Users/jasonczech/.codex/generated_images/01a05eb5-16da-7340-990d-d53e9fa49ed7/exec-1ecf81a0-e211-4aec-849e-6b9f0e491950.png` (comparison board containing the browser-rendered implementation capture)
- Responsive before/after evidence: `/Users/jasonczech/.codex/generated_images/01a05eb5-16da-7340-990d-d53e9fa49ed7/exec-dd7c795d-69c9-48da-a1e1-b5cd7af5a573.png`
- Source pixels: 1488 × 1056.
- Browser capture pixels: 1778 × 1250; browser CSS viewport reported 844 × 1000 because the in-app browser letterboxed its fixed content viewport. The comparison board normalized visible worksheet heights and cropped to the Scenario Builder content region. No density-based finding was filed.
- State: Stepped Ramp selected; 60-minute duration; 0–60 VUsers; six equal steps; scenario scope; early stop enabled at retained 5% / 1m values; model preview and six-row level table visible.

## Findings

No actionable P0, P1, or P2 differences remain.

- The implementation intentionally retains the existing APEX Load shell, six-step scenario wizard, theme tokens, typography, and shared form components. These differ from the standalone three-step mockup but preserve its worksheet hierarchy and are product constraints rather than design drift.
- P3: the calculation is presented as one compact result line instead of the mockup's two icon-led rows. It still exposes range, step count, VUsers per step, and equal interval duration without adding visual weight.
- P3: the archive/live evidence note replaces the mockup's total summary footer. The stage table still exposes all six targets and durations, while the chart's existing legend provides group and aggregate series.

## Required Fidelity Surfaces

- Fonts and typography: Existing APEX Load font family and optical weights are retained. Heading, section title, field label, result, and helper-text hierarchy remain clear with no truncation in the verified states.
- Spacing and layout rhythm: The worksheet preserves the numbered three-section progression, compact field groups, two-option controls, action placement, and a sticky preview. Desktop uses a two-column form/preview composition; the corrected narrow layout stacks the preview and expands the form to the available width.
- Colors and visual tokens: Existing APEX Load light/dark tokens and purple accent are used consistently. Selected, focused, disabled, warning, and validation states remain distinguishable.
- Image quality and asset fidelity: The target contains no product imagery requiring replacement. The implementation reuses the existing Recharts preview and existing APEX brand assets; no placeholder, CSS-art, emoji, or custom inline-SVG substitute was introduced.
- Copy and content: Worksheet copy explains the performance-at-each-level purpose, equal intervals, partial final steps, scenario/group scope, early-stop semantics, and retained evidence. Goal Oriented is absent from this flow.

## Full-view Comparison Evidence

The combined source/implementation board shows the same dominant structure: guided worksheet on the left, live staircase preview on the right, three numbered sections, range inputs, increment-mode choice, scenario/group scope, error protection, and a six-row level table. The implementation is denser because it lives inside the existing Scenario Builder shell; this is intentional and remains readable.

## Focused Region Comparison Evidence

Focused checks covered the calculation/result block, early-stop controls, and load-level table. The implementation reports `0 → 60 VUsers`, `6 steps`, `+10 VUsers per step`, and `10m each`; the preview table reports 10–60 VUsers in six 10-minute intervals. A partial final-step state at 55 VUsers was also captured and verified.

## Interaction and Accessibility Evidence

- Keyboard activation switched both increment modes.
- Invalid seven-step duration displayed nearby valid duration suggestions and disabled Apply.
- Changing the ending load to 55 immediately updated the preview and exposed the partial-step warning.
- Enabling early stop enabled the threshold and sustained-window controls.
- Scenario and group-scope controls use radio semantics; early stop uses a labeled checkbox.
- Browser console check returned no warnings or errors.

## Comparison History

1. P2 responsive finding: the higher-specificity desktop grid selector kept 520px and 380px tracks at the narrow breakpoint, squeezing the preview beside the form.
   - Fix: added an equal-specificity narrow override in `dashboard/src/components/TestHub.css`, stacked the preview, removed sticky/max-height behavior, and collapsed stepped-ramp field/protection grids to one column.
   - Post-fix evidence: the same browser state reported a single 526.9px workspace track; main and preview widths both measured 526.9px, with the preview positioned statically below the worksheet. The before/after artifact is linked above.

## Implementation Checklist

- [x] Desktop worksheet and sticky preview match Option 3's hierarchy.
- [x] Narrow layout stacks cleanly without a squeezed second track.
- [x] Default, partial-step, invalid-duration, and early-stop states are functional.
- [x] Keyboard and semantic-control behavior verified.
- [x] Browser console is clean.

final result: passed
