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
      ...reactHooks.configs.recommended.rules,
      '@typescript-eslint/no-explicit-any': 'off',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
    },
  },
)
