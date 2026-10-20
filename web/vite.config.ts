import { fileURLToPath, URL } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

const api = process.env.PAPERGO_API ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: !process.env.VITEST }), react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      // The workflow SDK's declarations, for the script editor.
      '@sdk': fileURLToPath(new URL('../sdk/dist', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    fs: { allow: ['.', '../sdk/dist'] },
    proxy: {
      '/v1': { target: api, changeOrigin: false },
      '/health': { target: api, changeOrigin: false },
      '/auth': { target: api, changeOrigin: false },
    },
  },
  // Monaco's editor chunk loads only with the script editor.
  build: { outDir: 'dist', chunkSizeWarningLimit: 3000 },
  test: { include: ['src/**/*.test.{ts,tsx}'], environment: 'node' },
});
