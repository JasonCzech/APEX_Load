// The "Tab moves focus" toggle for the script editors' code panes; the
// preference itself lives in ScriptEditorTabFocusPreference.

interface EditorTabFocusToggleProps {
  tabMovesFocus: boolean
  onChange: (enabled: boolean) => void
}

export function EditorTabFocusToggle({ tabMovesFocus, onChange }: EditorTabFocusToggleProps) {
  return (
    <button
      type="button"
      className="btn btn-sm btn-ghost editor-tab-focus-toggle"
      aria-pressed={tabMovesFocus}
      title="When on, Tab moves focus out of code editors instead of indenting"
      onClick={() => onChange(!tabMovesFocus)}
    >
      Tab moves focus: {tabMovesFocus ? 'On' : 'Off'}
    </button>
  )
}
