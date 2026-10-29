var e=`# Object activity and archiving

The project **Activity** page retains disclosure-safe identity, object name, time,
action, outcome, and changed section names for scripts, scenarios, runs, and imported
results. Native results share their run's identity. Deleted objects keep their
history and are marked as deleted. Raw script content, parameters, credentials,
notes, and administrative audit details are never returned through this view.
Historical attribution without a reliable identity is shown as **Unknown**.

Script and scenario libraries default to **Active**. Choose **Archived** or **All**
to inspect archived objects and restore them. Archive and restore require the same
permissions as editing, with the object's current \`updated_at\` version. Repeated
requests for an already-applied state do not create another transition event.
Archiving preserves IDs, references, content, and schedules; viewing, history,
export, restoration, and authorized deletion remain available. Restore an object
before editing or replacing it through an import.

Archived scenarios and scripts block new execution, including prepared starts,
CI library scripts, replay/debug sessions, and execution plans. Run setup identifies
archived dependencies. Existing running tests continue. Scheduled occurrences
blocked by archiving are skipped with \`object_archived\`; restoration allows future
occurrences without replaying skipped ones. Run/result artifact publication remains
automatic and is attributed to the executing system, with an initiating user when
known.

Restoration persists a scheduling boundary. Overdue occurrences are skipped even
if the controller was stopped throughout the archive interval, so catch-up policies
cannot replay work missed while an object was archived.

Project readers can query the safe activity API; project and resource scopes are
applied before filtering and pagination. Administrative audit endpoints retain their
existing permissions. See the generated [OpenAPI](swagger.yaml) and
[endpoint catalog](api-endpoint-catalog.json) for routes and request contracts.

Ordinary changes remain saved if activity persistence fails. API responses expose
\`X-APEXLOAD-Audit-Status: unavailable\`, and the dashboard displays “Change saved;
activity history could not be recorded.” Background failures produce operational
warnings. Required credential and policy auditing still enforces its existing
fail-closed behavior. Activity follows the configured audit retention policy, which
retains history indefinitely by default.
`;export{e as default};