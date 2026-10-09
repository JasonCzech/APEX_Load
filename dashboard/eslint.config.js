import js from '@eslint/js'
import globals from 'globals'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

const blockingA11yRules = {
  // These rules identify objective markup defects with a low false-positive
  // rate. Keep them in the default lint gate while the interaction-heavy
  // legacy ruleset is paid down separately.
  'jsx-a11y/alt-text': 'error',
  'jsx-a11y/aria-props': 'error',
  'jsx-a11y/aria-proptypes': 'error',
  'jsx-a11y/aria-unsupported-elements': 'error',
  'jsx-a11y/heading-has-content': 'error',
  'jsx-a11y/role-has-required-aria-props': 'error',
  'jsx-a11y/role-supports-aria-props': 'error',
}

const legacyA11yWarningRules = Object.fromEntries(
  Object.entries(jsxA11y.flatConfigs.recommended.rules).map(([name, configuration]) => {
    if (name in blockingA11yRules) return [name, 'off']
    if (configuration === 'off' || configuration === 0) return [name, configuration]
    if (Array.isArray(configuration)) return [name, ['warn', ...configuration.slice(1)]]
    return [name, 'warn']
  }),
)

// Labels in the settings editors commonly wrap a descriptive span and a
// reusable input component. Inspect deeply enough to recognize the resulting
// native control instead of reporting those valid wrapped-label associations.
legacyA11yWarningRules['jsx-a11y/label-has-associated-control'] = ['warn', {
  labelComponents: [],
  labelAttributes: [],
  controlComponents: ['SecondsFromMillisecondsInput', 'ActionBodyTextarea', 'TimezoneSelect', 'IntegerField'],
  assert: 'either',
  depth: 25,
}]
legacyA11yWarningRules['jsx-a11y/no-noninteractive-tabindex'] = ['warn', {
  tags: [],
  // Adjustable separators and keyboard-navigable regions intentionally enter
  // the tab order and provide their own key handlers.
  roles: ['tabpanel', 'separator', 'region'],
}]

const includeLegacyA11yWarnings = process.env.APEXLOAD_A11Y_LEGACY === '1'

export default tseslint.config(
  {
    ignores: ['dist', '../pkg/api/static'],
  },
  {
    files: ['scripts/**/*.mjs'],
    extends: [js.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.node,
      sourceType: 'module',
    },
  },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      'jsx-a11y': jsxA11y,
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...(includeLegacyA11yWarnings ? legacyA11yWarningRules : {}),
      ...blockingA11yRules,
      // v7's `recommended` preset now enables React Compiler diagnostics in
      // addition to the two correctness rules used by the previous major.
      // Keep this dependency-only upgrade behaviorally stable; compiler-rule
      // adoption belongs in a dedicated cleanup with its own migration plan.
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      '@typescript-eslint/no-explicit-any': 'off',
      'react-refresh/only-export-components': ['warn', {
        allowConstantExport: true,
        // Pure model helpers are intentionally colocated with the prototype
        // until that screen is decomposed; they do not hold React state.
        allowExportNames: [
          'buildProjectDashboardWorkspace',
          'stabilizeErrorRowOrder',
          'chartTimeAxisPresentation',
          'formatChartTimeMetricValue',
          'formatMetricTableValue',
          'historicalGroupSummaryQuery',
          'selectedGroupHistoryHasFields',
          'selectedGroupLoadImpactData',
          'buildSelectedGroupChartHistoryFromTransactions',
          'defaultImportSurfaces',
          'buildRunCalendarModel',
          'mergeDashboardService',
        ],
      }],
    },
  },
)
