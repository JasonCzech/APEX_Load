# Add JMeter Script Import as a First-Class Migration Path

## Summary
Introduce a JMeter import workflow that allows users to upload `.jmx` test plans and convert them into APEX Load reusable assets.

The first release should focus on HTTP-oriented JMeter plans and produce:
- Reusable APEX Load scripts
- Optional scenario drafts derived from JMeter thread groups
- A structured import report with warnings, unsupported constructs, and assumptions

This should sit alongside the existing LoadRunner import path and position APEX Load as a realistic migration target for teams with existing JMeter assets.

## Current Status
- `POST /api/v1/scripts/import/jmeter` is implemented for preview conversion.
- `POST /api/v1/scripts/import/jmeter/validate` is implemented for dry-run validation.
- `POST /api/v1/scripts/import/jmeter/save` persists reviewed scripts and optional scenario drafts.
- `GET /api/v1/scripts/import/jmeter/support-matrix` is implemented and used by the dashboard.
- The Scripts page now exposes an `Import JMeter` entry with both `Validate Only` and `Convert JMeter Plan` actions.
- Generated scripts now include JMeter import provenance metadata: original file name, import timestamp, warning count, and unsupported count.
- Sampler-level regex, JSONPath, JMESPath, CSS/JQuery, XPath, and XPath2 post-processors are mapped to APEX Load action extractors when they fit the DSL directly, including header-scoped regex extraction for HTTP responses, XPath fragment output, and XPath2 custom namespace bindings.
- Sampler-level boundary extractors are mapped to APEX Load action extractors for supported HTTP response scopes, including response body and response headers.
- The import review now surfaces captured variables from generated action extractors instead of leaving them buried in raw JSON preview.
- The support matrix now exposes inline category details on the import screen instead of only showing summary-card previews.
- The dashboard now has component-level Vitest coverage for the JMeter import screen, including inline coverage details and validation review rendering.
- `examples/jmeter-http-smoke.jmx` is available as a manual validation fixture for the end-to-end import flow.
- `examples/jmeter-http-extractors.jmx` is available as a manual validation fixture for extractor-focused review coverage.

### Remaining Phase 1 Gaps
- Add extractor families and modes that still need new runtime support or lossy mapping decisions, such as non-HTTP JMeter variants.

## Goals
- Reduce onboarding friction for existing performance teams.
- Convert a practical subset of JMeter plans into usable APEX Load artifacts.
- Make unsupported constructs explicit through warnings rather than silent failure.
- Preserve parameterization and thread-group intent where possible.
- Keep the first version narrow and reliable rather than broad and misleading.

## Non-Goals for Phase 1
- Full support for all JMeter plugins
- Groovy / BeanShell / JSR223 execution migration
- Distributed JMeter controller settings
- Full fidelity controller tree behavior across all JMeter elements
- Non-HTTP protocols in the initial pass
- ZIP bundle dependency resolution in the first cut unless needed for CSV/data files

## Phase 1 Scope
Support upload of `.jmx` files and convert common HTTP-focused JMeter plans.

### Supported First-Wave Elements
- Test Plan
- Thread Group
- HTTP Request Defaults
- HTTP Header Manager
- HTTP Cookie Manager
- User Defined Variables
- CSV Data Set Config
- HTTP Request samplers
- Constant Timer
- Uniform Random Timer
- Response Assertions
- Common extractors where mapping is straightforward

### Partially Supported
- Simple controllers if they can be flattened safely
- Loop behavior where it can be mapped to scenario/runtime settings
- Basic variable substitution across URLs, headers, and bodies

### Explicitly Unsupported in Phase 1
- JSR223 / BeanShell / Groovy scripting
- Plugin samplers/listeners/controllers
- JMS samplers from JMeter
- JDBC / FTP / SOAP / custom protocol plugins
- Complex conditional controllers with embedded logic
- Full JMeter listener/reporting model
- Distributed engine/controller orchestration settings

## UX Model
Add a new import path under Scripts:

- Import LoadRunner
- Import JMeter
- Existing native creation flows remain unchanged

### Import Flow
1. User selects Import JMeter
2. Uploads `.jmx`
3. Chooses import mode:
   - Scripts Only
   - Scripts + Scenario Draft
4. APEX Load parses and converts the plan
5. UI shows an Import Review screen:
   - scripts/groups created
   - variables and data sources found
   - warnings
   - unsupported constructs
   - assumptions made
6. User confirms save

## Conversion Model

### JMeter to APEX Load Script Mapping
- JMeter HTTP samplers map to APEX Load HTTP request actions
- Header Manager maps to request headers
- Cookie Manager maps to cookie/session behavior
- CSV Data Set Config maps to APEX Load data sources
- User Defined Variables maps to APEX Load variables/templates
- Assertions map to response validations where possible
- Extractors map to DSL extraction blocks when supported

### Thread Groups
Thread groups should become one of:
- separate reusable APEX Load scripts, or
- grouped scenario draft entries with:
  - name
  - VUsers
  - ramp-up hints
  - timing assumptions

Recommended first implementation:
- Create reusable script artifacts
- Optionally create a scenario draft that uses those scripts as grouped workload components

### Variable Handling
Map JMeter `${variable}` references into APEX Load-compatible variable expressions.

Examples:
- request URL parameters
- headers
- body payload fields
- think-time or extracted values where feasible

### Timers and Pacing
- Map simple timers into pacing/wait actions
- Record unsupported timing behavior as warnings if fidelity is uncertain

## Import Report Contract
Every import should return:
- generated scripts
- optional scenario draft
- `warnings[]`
- `unsupported_elements[]`
- `assumptions[]`
- metadata summary:
  - file name
  - thread group count
  - sampler count
  - variables discovered
  - CSV sources discovered

### Example Warning Types
- unsupported controller skipped
- script logic not convertible
- timer converted approximately
- assertion downgraded to warning-only
- external file reference unresolved

## API Changes

### New Endpoints
- `POST /api/v1/scripts/import/jmeter`
  - upload `.jmx`
  - returns converted assets + import report preview
- `POST /api/v1/scripts/import/jmeter/save`
  - persists reviewed import result as scripts and optional scenario draft

### Optional Follow-Up Endpoints
- `POST /api/v1/scripts/import/jmeter/validate`
  - parse and inspect without producing persisted output
- `GET /api/v1/scripts/import/jmeter/support-matrix`
  - returns supported / partial / unsupported construct catalog

## Backend Implementation Plan

### 1. Parser Layer
Add a JMeter import package, for example:
- `pkg/convert/jmeter`

Responsibilities:
- parse JMX XML
- build normalized intermediate model
- capture raw element type inventory

### 2. Normalized Import Model
Create internal structs for:
- test plan
- thread groups
- samplers
- headers
- cookies
- variables
- CSV configs
- timers
- assertions
- extractors
- unsupported nodes

### 3. Converter Layer
Map normalized JMeter model into:
- APEX Load DSL script model
- scenario draft model
- import report model

### 4. Save Layer
Persist:
- scripts
- optional scenario draft
- import metadata

### 5. Provenance Metadata
Store import metadata on generated scripts:
- `generated_by: jmeter-import`
- original file name
- import timestamp
- warning count
- unsupported count

## Frontend Implementation Plan

### Scripts Page
Add a new import lane/card:
- `Import JMeter`

### JMeter Import UI
Build:
- upload form
- import-mode selector
- validate-only dry-run action
- parsed summary panel
- warning/unsupported list
- generated script preview list
- save confirmation step

### Review Experience
The user should be able to see:
- what was created
- what was approximated
- what was not supported

This is important so migration feels trustworthy.
