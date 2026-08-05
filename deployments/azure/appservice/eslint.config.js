const js = require('@eslint/js');
const globals = require('globals');

module.exports = [
  {
    ignores: ['bin', 'node_modules'],
  },
  {
    ...js.configs.recommended,
    files: ['*.js'],
    languageOptions: {
      ...js.configs.recommended.languageOptions,
      ecmaVersion: 2022,
      globals: globals.node,
      sourceType: 'commonjs',
    },
  },
];
