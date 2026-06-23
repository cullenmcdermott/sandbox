// Flat ESLint config for the runner. Lints src/ only (see the "lint" script).
// Minimal ruleset per the harness-engineering plan: typescript-eslint
// recommended + the type-aware no-floating-promises rule. Generated files
// (*.gen.ts) and build output are ignored.
//
// Not installed on the Nix host — run via CI (the hard gate) or locally after
// `npm install --ignore-scripts` with `npm run lint`.
import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['dist/', 'node_modules/', 'src/**/*.gen.ts', 'eslint.config.js'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.ts'],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      '@typescript-eslint/no-floating-promises': 'error',
    },
  },
);
