# Add JMeter Script Import as a First-Class Migration Path

## Summary
Introduce a JMeter import workflow that allows users to upload `.jmx` test plans and convert them into Stormrunner reusable assets.

The first release should focus on HTTP-oriented JMeter plans and produce:
- Reusable Stormrunner scripts
- Optional scenario drafts derived from JMeter thread groups
- A structured import report with warnings, unsupported constructs, and assumptions

This should sit alongside the existing LoadRunner import path and position Stormrunner as a realistic migration target for teams with existing JMeter assets.

## Goals
- Reduce onboarding friction for existing performance teams.
- Convert a practical subset of JMeter plans into usable Stormrunner artifacts.
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
4. Stormrunner parses and converts the plan
5. UI shows an Import Review screen:
   - scripts/groups created
   - variables and data sources found
   - warnings
   - unsupported constructs
   - assumptions made
6. User confirms save

## Conversion Model

### JMeter to Stormrunner Script Mapping
- JMeter HTTP samplers map to Stormrunner HTTP request actions
- Header Manager maps to request headers
- Cookie Manager maps to cookie/session behavior
- CSV Data Set Config maps to Stormrunner data sources
- User Defined Variables maps to Stormrunner variables/templates
- Assertions map to response validations where possible
- Extractors map to DSL extraction blocks when supported

### Thread Groups
Thread groups should become one of:
- separate reusable Stormrunner scripts, or
- grouped scenario draft entries with:
  - name
  - VUsers
  - ramp-up hints
  - timing assumptions

Recommended first implementation:
- Create reusable script artifacts
- Optionally create a scenario draft that uses those scripts as grouped workload components

### Variable Handling
Map JMeter `${variable}` references into Stormrunner-compatible variable expressions.

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
- Stormrunner DSL script model
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

## Suggested File Structure
- `pkg/convert/jmeter/parse.go`
- `pkg/convert/jmeter/model.go`
- `pkg/convert/jmeter/convert.go`
- `pkg/convert/jmeter/report.go`
- `pkg/api/handlers_import_jmeter.go`
- frontend import components under:
  - `dashboard/src/components/JMeterImport.tsx`
  - or integrated into the existing script import flow

## Validation Rules
Reject with clear errors when:
- file is not valid XML/JMX
- test plan contains no supported executable content
- import would create empty scripts only
- required references are missing and cannot be resolved safely

Warn but continue when:
- some nodes are unsupported
- some assertions are approximate
- some timing/controller behavior was flattened

## Test Plan

### Backend Tests
- Parse valid `.jmx`
- Reject malformed XML
- Convert simple HTTP plan to DSL
- Convert multi-thread-group plan to multiple scripts
- Convert CSV data set config into data sources
- Preserve variables in converted output
- Produce warnings for unsupported elements
- Save reviewed import results successfully

### API Tests
- upload preview endpoint
- save endpoint
- invalid file handling
- warning payload correctness
- scenario draft creation option

### Frontend Tests
- upload flow renders
- import review screen shows warnings and unsupported items
- save flow persists scripts
- save flow optionally creates scenario draft
- `npm run build`

### End-to-End Sample Coverage
Use representative JMeter examples:
- single HTTP sampler plan
- login flow with headers/cookies
- CSV-parameterized test plan
- multi-thread-group workload
- plan containing unsupported scripting/plugin elements

## Rollout Recommendation

### Phase 1
- `.jmx` upload
- HTTP-only support
- script generation
- optional scenario draft generation
- import review with warnings

### Phase 2
- `.zip` upload with CSV/assets
- broader controller support
- richer extractor/assertion mapping
- better parity for pacing and loops

### Phase 3
- advanced migration helpers
- import diff/re-import workflow
- support matrix UI
- deeper non-HTTP expansion where aligned with Stormrunner roadmap

## Assumptions
- JMeter import is primarily a migration feature, not a long-term execution format.
- Generated output should always become native Stormrunner assets.
- Explicit warning/reporting is more valuable than pretending to support full fidelity.
- HTTP migration is the most valuable first target.
