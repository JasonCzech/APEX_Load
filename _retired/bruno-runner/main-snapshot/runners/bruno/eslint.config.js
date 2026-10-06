const js = require('@eslint/js');
const globals = require('globals');

module.exports = [
  {
    ignores: ['node_modules', 'sbom.cdx.json'],
  },
  {
    ...js.configs.recommended,
    files: ['src/**/*.js', 'scripts/**/*.js', 'test/**/*.js'],
    languageOptions: {
      ...js.configs.recommended.languageOptions,
      ecmaVersion: 2022,
      globals: globals.node,
      sourceType: 'commonjs',
    },
  },
];
