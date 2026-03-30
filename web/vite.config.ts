import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    port: 5173,
    // In dev mode, proxy API requests to the Go server.
    proxy: {
      '/api': 'http://localhost:7070',
      '/media': 'http://localhost:7070',
      '/health': 'http://localhost:7070',
      '/artwork': 'http://localhost:7070'
    }
  }
});
