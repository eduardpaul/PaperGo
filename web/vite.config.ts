import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath, URL } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import react from '@vitejs/plugin-react';
import type { Plugin } from 'vite';
import { defineConfig } from 'vitest/config';

const api = process.env.PAPERGO_API ?? 'http://127.0.0.1:8080';

// PDF.js reads CMaps, standard fonts, ICC profiles and its image decoders
// from directories at run time. They ship as versioned assets beside the
// bundle, which the PDF viewer names in getDocument.
const pdfjs = fileURLToPath(new URL('./node_modules/pdfjs-dist', import.meta.url));
const pdfjsAssets = `assets/pdfjs-${JSON.parse(readFileSync(join(pdfjs, 'package.json'), 'utf8')).version}/`;
const pdfjsDirectories = ['cmaps', 'standard_fonts', 'iccs', 'wasm'];

function pdfjsFiles(): Plugin {
  return {
    name: 'pdfjs-files',
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const [directory, name, ...rest] = (request.url ?? '').split('?')[0].replace(`/${pdfjsAssets}`, '').split('/');
        if (!request.url?.startsWith(`/${pdfjsAssets}`) || rest.length || !pdfjsDirectories.includes(directory) || !readdirSync(join(pdfjs, directory)).includes(name)) return next();
        response.setHeader('Content-Type', name.endsWith('.wasm') ? 'application/wasm' : 'application/octet-stream');
        response.end(readFileSync(join(pdfjs, directory, name)));
      });
    },
    generateBundle() {
      for (const directory of pdfjsDirectories)
        for (const name of readdirSync(join(pdfjs, directory)))
          this.emitFile({ type: 'asset', fileName: `${pdfjsAssets}${directory}/${name}`, source: readFileSync(join(pdfjs, directory, name)) });
    },
  };
}

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: !process.env.VITEST }), react(), tailwindcss(), pdfjsFiles()],
  define: { __PDFJS_ASSETS__: JSON.stringify(pdfjsAssets) },
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
  // Monaco's editor chunk loads only with the script editor, PDF.js only with the PDF viewer.
  build: { outDir: 'dist', chunkSizeWarningLimit: 3000 },
  test: { include: ['src/**/*.test.{ts,tsx}'], environment: 'node' },
});
