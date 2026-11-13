import js from '@eslint/js';
import ts from 'typescript-eslint';
import solid from 'eslint-plugin-solid';
export default ts.config(
  { ignores: ['dist/**', 'src/lib/api/generated/**', 'playwright-report/**', 'test-results/**'] },
  js.configs.recommended,
  ...ts.configs.recommended,
  solid.configs['flat/typescript'],
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: {
      globals: {
        window: 'readonly',
        document: 'readonly',
        fetch: 'readonly',
        FormData: 'readonly',
        HTMLFormElement: 'readonly',
        SubmitEvent: 'readonly',
        BeforeUnloadEvent: 'readonly',
        AbortSignal: 'readonly',
        DOMException: 'readonly',
        Response: 'readonly',
        TextEncoder: 'readonly',
        Intl: 'readonly',
      },
    },
  },
);
