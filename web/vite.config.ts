import { fileURLToPath, URL } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

const api = process.env.PAPERGO_API ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: !process.env.VITEST }), react(), tailwindcss()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: {
    port: 5173,
    proxy: {
      '/v1': { target: api, changeOrigin: false },
      '/health': { target: api, changeOrigin: false },
    },
  },
  build: { outDir: 'dist', chunkSizeWarningLimit: 1500 },
  test: { include: ['src/**/*.test.{ts,tsx}'], environment: 'node' },
});
