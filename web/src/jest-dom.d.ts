// jest-dom's matchers (toBeInTheDocument, toHaveTextContent, …) for Vitest 5.
//
// test-setup.ts registers them at runtime. Their types used to reach
// `expect()` through Vitest 4's Jest-compatible globals; Vitest 5 dropped
// those and changed Assertion<T> to Assertion<R, T>, and jest-dom 7.0.1 (the
// latest) still augments the old shape. Vitest 5's Assertion<R, T> extends
// Matchers<R, T>, so the matchers go there. Remove this file once jest-dom
// ships Vitest 5 types.
//
// The deep path is because jest-dom's package exports don't expose the
// matchers' interface; its own vitest.d.ts imports it the same way.
import type { TestingLibraryMatchers } from '../node_modules/@testing-library/jest-dom/types/matchers';

declare module 'vitest' {
  interface Matchers<R extends void | Promise<void> = void | Promise<void>, T = unknown>
    extends TestingLibraryMatchers<unknown, R> {}
}
