import { mergeAppViewWithWorkspace, parseAppViewFromHash, viewToLocationHash, workspaceScopeForAppView } from '../appRouting'

// Where a project scenario draft opens in the Scenario Builder. A draft
// belongs to one project, so the link names it; the current application and
// release carry over only while they are that project's.
export function scenarioDraftBuilderHash(draftId: string, projectId: string): string {
  const scope = workspaceScopeForAppView(parseAppViewFromHash(window.location.hash))
  return viewToLocationHash(mergeAppViewWithWorkspace({ page: 'tests', scenarioView: 'builder', scenarioDraftId: draftId, projectId }, scope))
}
