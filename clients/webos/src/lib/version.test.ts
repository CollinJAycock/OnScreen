import { describe, expect, it } from 'vitest';
import appinfo from '../../appinfo.json';
import pkg from '../../package.json';
import lock from '../../package-lock.json';
import { APP_VERSION } from './version';

// The version lives in four places (see version.ts); the store reads
// appinfo.json, Settings → About shows APP_VERSION.
describe('APP_VERSION', () => {
  it('matches appinfo.json, package.json and package-lock.json', () => {
    expect(appinfo.version).toBe(APP_VERSION);
    expect(pkg.version).toBe(APP_VERSION);
    expect(lock.version).toBe(APP_VERSION);
    expect(lock.packages[''].version).toBe(APP_VERSION);
  });
});
