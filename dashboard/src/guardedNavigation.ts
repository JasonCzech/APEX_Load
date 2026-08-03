export function commitGuardedNavigation(
  runGuarded: (proceed: () => void) => void,
  commit: () => void,
  afterCommit?: () => void,
): void {
  runGuarded(() => {
    commit()
    afterCommit?.()
  })
}
