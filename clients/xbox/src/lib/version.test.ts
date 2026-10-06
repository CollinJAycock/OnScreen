import { describe, expect, it } from 'vitest';
import pkg from '../../package.json';
import lock from '../../package-lock.json';
import { APP_VERSION } from './version';

// The version lives in three places (see version.ts); Settings > About shows
// APP_VERSION.
describe('APP_VERSION', () => {
  it('matches package.json and package-lock.json', () => {
    expect(pkg.version).toBe(APP_VERSION);
    expect(lock.version).toBe(APP_VERSION);
    expect(lock.packages[''].version).toBe(APP_VERSION);
  });
});
