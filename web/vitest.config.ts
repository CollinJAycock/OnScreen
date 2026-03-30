import { defineConfig } from 'vitest/config';

const root = new URL('.', import.meta.url).pathname;

export default defineConfig(async () => {
  const { svelte } = await import('@sveltejs/vite-plugin-svelte');
  return {
    plugins: [svelte({ hot: false })],
    resolve: {
      conditions: ['browser']
    },
    test: {
      include: ['src/**/*.test.ts'],
      environment: 'happy-dom',
      globals: true,
      setupFiles: ['src/test-setup.ts'],
      alias: {
        $lib: root + 'src/lib',
        '$app/navigation': root + 'src/__mocks__/app-navigation.ts',
        '$app/stores': root + 'src/__mocks__/app-stores.ts'
      }
    }
  };
});
