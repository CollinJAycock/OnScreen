import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [sveltekit()],
  build: {
    target: 'es2019',
    cssCodeSplit: false
  },
  server: {
    fs: { strict: false }
  }
});
