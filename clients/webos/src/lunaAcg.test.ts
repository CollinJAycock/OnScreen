import { describe, expect, it } from 'vitest';
import appinfo from '../appinfo.json';

// Every Luna service the app calls needs its ACG group in appinfo.json's
// requiredACG: webOS TV 26 enforces the list for an app that declares it,
// and from webOS TV 27 for every app, failing an undeclared call with
// errorCode -1 ("Denied method call"). See README "Luna permissions (ACG)".
//
// This scans the shipped sources (src/, tests aside) for luna:// and
// palm:// URIs, and for the webOSTV.js calls that make Luna calls with no
// URI in our code. The groups of each come from the map below, kept by
// hand: LG's table is the reference, and a call the map doesn't know fails
// until someone has looked its groups up.

/** Luna method URI (or webOSTV.js call, as `webOS.deviceInfo()`) → its ACG
 *  groups. */
const ACG_GROUPS: Record<string, string[]> = {
  // The HDR / UHD panel probe (lib/platform.ts). Not in LG's table: the
  // group configd assigns it, and the one webOSTV.js deviceInfo() declares.
  'luna://com.webos.service.config/getConfigs': ['systemconfig.query'],
};

const README_HINT =
  'Look the method up in LG\'s ACG guide as README "Luna permissions (ACG)" says, ' +
  'add its groups to requiredACG in appinfo.json and the call to ACG_GROUPS in src/lunaAcg.test.ts.';

// Every source file under src/ as text (the test files ship nowhere).
const sources = import.meta.glob(['./**/*.{ts,js,svelte,html}', '!./**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

// A service URI as written in a string: luna://com.webos.service.x/method.
// One built from pieces ('luna://com.x/' + method, or `luna://${service}`)
// is caught by its prefix, down to a bare 'luna://', and fails as unknown:
// write the whole URI out.
const URI_RE = /\b(?:luna|palm):\/\/[\w.\-/]*/g;

// webOSTV.js calls that make Luna calls themselves: webOS.deviceInfo(),
// webOS.service.request() (whose method rides in its options) and anything
// on webOSDev (webOSTV-dev.js: LGUDID, DRM, connection status). Not
// webOSSystem.deviceInfo or webOS.platformBack(), which aren't Luna calls.
const HELPER_RE = /\bwebOS\??\.(?:deviceInfo|service\??\.request)\b|\bwebOSDev\??\.\w+/g;

/** Each Luna call found (a URI, or a webOSTV.js call as `webOS.x()` /
 *  `webOSDev.x`) → the files it's in. */
function serviceUris(texts: Record<string, string> = sources): Map<string, string[]> {
  const found = new Map<string, string[]>();
  const add = (call: string, file: string) => {
    const files = found.get(call) ?? [];
    if (!files.includes(file)) files.push(file);
    found.set(call, files);
  };
  for (const [file, text] of Object.entries(texts)) {
    // Drops a trailing slash off the path, never the scheme's own '//'.
    for (const m of text.match(URI_RE) ?? []) add(m.replace(/^(\w+:\/\/.*?[^/])\/+$/, '$1'), file);
    for (const m of text.match(HELPER_RE) ?? []) {
      const call = m.replace(/\?/g, '');
      add(call.startsWith('webOSDev.') ? call : `${call}()`, file);
    }
  }
  return found;
}

const declared: unknown = (appinfo as { requiredACG?: unknown }).requiredACG;

describe('Luna permissions (ACG)', () => {
  it('declares requiredACG as a list of group names (required from webOS TV 27)', () => {
    expect(Array.isArray(declared)).toBe(true);
    for (const g of declared as unknown[]) expect(typeof g).toBe('string');
  });

  it('scans the app sources (the panel probe is found)', () => {
    expect(Object.keys(sources).some((f) => f.endsWith('.svelte'))).toBe(true);
    expect(serviceUris().get('luna://com.webos.service.config/getConfigs')).toEqual(['./lib/platform.ts']);
  });

  it('finds a URI built from pieces, and the webOSTV.js calls that make Luna calls', () => {
    const found = serviceUris({
      'a.ts': "bridge.call('luna://' + service + '/' + method, '{}');",
      'b.ts': 'bridge.call(`palm://${service}/${method}`, params);',
      'c.ts': "bridge.call('luna://com.webos.service.tv.systemproperty/', '{}');",
      'd.ts': 'window.webOS?.deviceInfo((info) => use(info));',
      'e.ts': "webOS.service.request('luna://com.webos.service.sm', { method: 'deviceid/getIDs' });",
      'f.ts': 'webOSDev.LGUDID({ onSuccess });',
      // Not Luna calls: the system object's property, and prose.
      'g.ts': 'const raw = webOSSystem.deviceInfo; // as webOSTV.js deviceInfo() does',
    });
    expect([...found.keys()].sort()).toEqual(
      [
        'luna://',
        'palm://',
        'luna://com.webos.service.tv.systemproperty',
        'webOS.deviceInfo()',
        'webOS.service.request()',
        'luna://com.webos.service.sm',
        'webOSDev.LGUDID',
      ].sort(),
    );
  });

  it('knows the ACG groups of every Luna call in src/', () => {
    const unknown = [...serviceUris()]
      .filter(([uri]) => !(uri in ACG_GROUPS))
      .map(([uri, files]) => `${uri} (in ${files.join(', ')}) has no ACG group here. ${README_HINT}`);
    expect(unknown).toEqual([]);
  });

  it('declares the groups of every Luna call in src/ in appinfo.json', () => {
    const groups = new Set(declared as string[]);
    const missing: string[] = [];
    for (const [uri, files] of serviceUris()) {
      for (const g of ACG_GROUPS[uri] ?? []) {
        if (groups.has(g)) continue;
        missing.push(
          `requiredACG in appinfo.json lacks "${g}", which ${uri} (in ${files.join(', ')}) needs: ` +
            'add it, or the call is denied on webOS TV 26 and later.',
        );
      }
    }
    expect(missing).toEqual([]);
  });

  it('declares no group that no call in src/ needs', () => {
    const needed = new Set([...serviceUris().keys()].flatMap((uri) => ACG_GROUPS[uri] ?? []));
    const stale = (declared as string[])
      .filter((g) => !needed.has(g))
      .map((g) => `requiredACG in appinfo.json declares "${g}", which no Luna call in src/ needs: drop it.`);
    expect(stale).toEqual([]);
  });
});
