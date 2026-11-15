import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { themeFontAliases } from '../theme-fonts.config.ts';
export default defineConfig({
  root: 'preview',
  plugins: [react()],
  resolve: { alias: themeFontAliases },
  server: { host: '127.0.0.1', port: 5174, fs: { allow: ['..'] } },
  build: { outDir: '../dist/astryx-preview', emptyOutDir: true },
});
