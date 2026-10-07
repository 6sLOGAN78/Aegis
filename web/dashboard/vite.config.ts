import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// https://vite.dev/config/
export default defineConfig({
  base: '/dashboard/',
  plugins: [
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
