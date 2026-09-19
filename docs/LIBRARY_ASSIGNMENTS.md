# Script and scenario application/release assignments

In the Scripts or Scenarios library, open an item's **More** menu and choose
**Change application/releases…**. Select an application and any existing releases
in the current project, then apply. Selecting **Unassigned (clear assignment)**
removes both the application and releases. An application can also be assigned
without any releases.

Select several local items to use the same action in the selection toolbar.
Bulk changes replace each item's complete assignment. The dialog previews the
replacement, skips unchanged items, and reports individual failures. Retry only
retries failed items. If another operator has changed an assignment, reload the
conflicting assignment and review it before applying again.

The **Assigned application/releases** column and assignment filters describe
editable library metadata. **Run releases** describes historical run associations.
Archived releases can remain assigned but cannot be newly assigned or selected
for a new launch. Repository browsing includes assigned items before their first
run and distinguishes assignments from run history.

Script changes require `manage_scripts`; scenario changes require `manage_tests`.
Assignments remain independent between a scenario and its scripts. They do not
change script definitions, scenario groups, existing runs, or saved schedules.
New and cloned assets begin unassigned.

For a new launch, the scenario's application is suggested. If it is unassigned,
explicitly assigned scripts suggest an application only when they agree. Assigned
releases are labeled in the release selector. Choose a release explicitly for
each fresh launch or new schedule, even when only one is assigned. You may choose
another application or release; changing application clears the pending release.
Restoring a saved draft preserves its chosen release.

The replacement API uses an assignment revision to reject stale writes. See the
[generated API specification](swagger.yaml) and
[endpoint catalog](api-endpoint-catalog.json) for the request and response contracts.
API and CLI execution clients retain their existing placement rules.
