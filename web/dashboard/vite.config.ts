import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// https://vite.dev/config/
export default defineConfig({
  base: '/dashboard/',
  plugins: [
    {
      // @monaco-editor/loader ships a default CDN path that is dead code once
      // loader.config({ monaco }) is set (see src/utils/monaco-setup.ts). Neutralize the
      // literal so the bundle contains no third-party CDN reference.
      name: 'strip-monaco-loader-cdn',
      enforce: 'pre',
      transform(code: string, id: string) {
        if (id.includes('@monaco-editor/loader') && code.includes('cdn.jsdelivr.net')) {
          return { code: code.replace(/https:\/\/cdn\.jsdelivr\.net\/npm\/monaco-editor@[^'"]*/g, '/dashboard/monaco-local/vs'), map: null };
        }
        return null;
      },
    },
    react(),
    tailwindcss(),
  ],
  server: {
    proxy: {
      '/control/v1': {
        target: 'http://localhost:8084',
        changeOrigin: true,
      },
    },
  },
});
