import { describe, expect, it } from 'vitest';
import pkg from '../../package.json';
import {
  APP_LICENCE,
  LICENCE_FILE,
  MIT_LICENCE,
  NOTICES,
  PRIVACY_POLICY_URL,
  SUPPORT_EMAIL,
  TERMS,
  allNotices,
  apacheTerms,
  usedUnder,
} from './legal';

// The project has no Node typings, so node:fs and node:crypto come in
// untyped.
const fsModule = 'node:fs';
const cryptoModule = 'node:crypto';
const { existsSync, readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  existsSync(path: URL): boolean;
  readFileSync(path: URL, encoding: 'utf8'): string;
};
const { createHash } = (await import(/* @vite-ignore */ cryptoModule)) as {
  createHash(alg: string): { update(s: string): { digest(enc: 'hex'): string } };
};

/** A file under clients/webos, as text with LF line ends (a Windows
 *  checkout has CRLF). */
const read = (path: string) =>
  readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8').replace(/\r\n/g, '\n');

// sha256 of https://www.apache.org/licenses/LICENSE-2.0.txt, fetched
// 2026-10-02 (the widely published hash of the canonical text).
const APACHE_2_SHA256 = 'cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30';
const COPYRIGHT = 'Copyright 2026 Collin Aycock';

describe('the webOS client licence', () => {
  it('is Apache-2.0 in package.json, and in package-lock.json as npm writes it', () => {
    expect(pkg.license).toBe('Apache-2.0');
    const lock = JSON.parse(read('package-lock.json')) as { packages: Record<string, { license?: string }> };
    expect(lock.packages[''].license).toBe('Apache-2.0');
  });

  it('LICENSE is the copyright line, then the Apache License 2.0 verbatim', () => {
    const text = LICENCE_FILE.replace(/\r\n/g, '\n');
    expect(text.startsWith(`${COPYRIGHT}\n`)).toBe(true);
    const rest = text.slice(COPYRIGHT.length + 1);
    expect(createHash('sha256').update(rest).digest('hex')).toBe(APACHE_2_SHA256);
  });

  it('the in-app notice names the licence and the copyright', () => {
    expect(APP_LICENCE[0]).toBe(COPYRIGHT);
    expect(APP_LICENCE.join(' ')).toContain('Apache License, Version 2.0 (Apache-2.0)');
    expect(APP_LICENCE.join(' ')).toContain('"AS IS" BASIS');
  });
});

describe('apacheTerms', () => {
  it('runs from the title to the end of the terms, a paragraph each', () => {
    const terms = apacheTerms();
    expect(terms[0]).toBe('Apache License Version 2.0, January 2004 http://www.apache.org/licenses/');
    expect(terms[terms.length - 1]).toBe('END OF TERMS AND CONDITIONS');
    expect(terms).toContain('1. Definitions.');
    expect(terms.some((p) => p.startsWith('7. Disclaimer of Warranty.'))).toBe(true);
    expect(terms.some((p) => p.startsWith('8. Limitation of Liability.'))).toBe(true);
    // Without this app's copyright line (APP_LICENCE has it) or the appendix.
    expect(terms.join(' ')).not.toContain(COPYRIGHT);
    expect(terms.join(' ')).not.toContain('APPENDIX');
    // The hard-wrapped lines are joined.
    expect(terms.every((p) => !p.includes('\n'))).toBe(true);
  });

  it('reads a CRLF checkout the same', () => {
    expect(apacheTerms(LICENCE_FILE.replace(/\r?\n/g, '\r\n'))).toEqual(apacheTerms(LICENCE_FILE));
  });
});

describe('the end-user terms (LG Seller Lounge terms §7.1)', () => {
  const all = TERMS.join(' ');

  it('say the developer provides the app, not LG', () => {
    expect(all).toContain('provided by its developer, Collin Aycock, and not by LG Electronics');
    // Shown under Settings only, never before use: they apply, rather than
    // being accepted by use.
    expect(all).toContain('These terms apply to your use of the app.');
    expect(all).not.toContain('By using the app you accept');
  });

  it('exclude LG and its affiliates from responsibility and liability', () => {
    expect(all).toContain(
      'LG Electronics Inc. and its affiliates are not responsible for the app, its content, its availability or its support, and have no liability to you for it.',
    );
  });

  it('name LG and its affiliates as third-party beneficiaries', () => {
    expect(all).toContain('LG Electronics Inc. and its affiliates are third-party beneficiaries of these terms');
  });

  // Not "only" that server: Discover shows TMDB's posters straight from
  // image.tmdb.org (the server passes their URLs on), and Live TV and
  // Recordings channel logos from wherever the channel list says.
  it('say the app reaches the user’s own server and the images it points to, and carries no content', () => {
    expect(all).toContain('connects to your own OnScreen server, at the address you enter');
    expect(all).not.toContain('connects only to');
    expect(all).toContain('loads the images that server points it to');
    expect(all).toContain('film posters from TMDB in Discover');
    expect(all).toContain('channel logos in Live TV and Recordings');
    expect(all).toContain('It includes no films, shows, music or other content');
    expect(all).toContain('You are responsible for the media you add to your server');
  });

  it('disclaim warranties under Apache-2.0, note country limits, give a contact', () => {
    expect(all).toContain('provided "as is", without warranty of any kind, under the Apache License 2.0');
    expect(all).toContain('may not be available in every country');
    expect(all).toContain(SUPPORT_EMAIL);
    expect(SUPPORT_EMAIL).toBe('collin.j.aycock@gmail.com');
  });

  it('point at the privacy policy', () => {
    expect(PRIVACY_POLICY_URL).toBe('https://onscreen.wolverscreen.com/privacy');
    expect(all).toContain(PRIVACY_POLICY_URL);
  });
});

/** A Markdown link as the screen writes it: "text (url)". */
const unlink = (s: string) => s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '$1 ($2)');

/** An installed package's LICENSE file (LICENSE or LICENSE.md), as text. */
function installedLicence(name: string): string {
  for (const file of ['LICENSE', 'LICENSE.md']) {
    const url = new URL(`../../node_modules/${name}/${file}`, import.meta.url);
    if (existsSync(url)) return unlink(readFileSync(url, 'utf8').replace(/\r\n/g, '\n'));
  }
  throw new Error(`no LICENSE in node_modules/${name}`);
}

describe('third-party notices', () => {
  it('list hls.js under Apache-2.0', () => {
    const hls = NOTICES.find((n) => n.pkg === 'hls.js');
    expect(hls?.licence).toBe('Apache-2.0');
    expect(hls?.notice).toBe('Copyright (c) 2017 Dailymotion (http://www.dailymotion.com)');
  });

  it('list every runtime dependency, and the Svelte and SvelteKit runtimes', () => {
    const listed = NOTICES.map((n) => n.pkg);
    for (const dep of [...Object.keys(pkg.dependencies), 'svelte', '@sveltejs/kit']) {
      expect(listed, `${dep}: add it to NOTICES in src/lib/legal.ts and THIRD_PARTY_NOTICES.md`).toContain(dep);
    }
  });

  it("match each installed package's licence and copyright line", () => {
    for (const n of NOTICES) {
      if (!n.pkg) continue;
      const manifest = JSON.parse(read(`node_modules/${n.pkg}/package.json`)) as { license: string };
      expect(manifest.license, n.pkg).toBe(n.licence);
      expect(installedLicence(n.pkg), n.pkg).toContain(n.notice);
    }
  });

  it("carry every copyright line in hls.js's own LICENSE (the code it derives from videojs-contrib-hls too)", () => {
    const hls = NOTICES.find((n) => n.pkg === 'hls.js');
    const listed = hls ? [hls, ...(hls.includes ?? [])].map((n) => n.notice) : [];
    const lines = installedLicence('hls.js')
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => /^Copyright\b/.test(l));
    expect(lines).toContain('Copyright (c) 2013-2015 Brightcove');
    for (const line of lines) expect(listed, `${line}: add it to hls.js's includes`).toContain(line);
  });

  it('are the same in THIRD_PARTY_NOTICES.md', () => {
    const md = read('THIRD_PARTY_NOTICES.md');
    // Each entry's own section: its heading to the next one.
    const sections = new Map<string, string>();
    for (const part of md.split(/^#{2,3} /m).slice(1)) {
      const nl = part.indexOf('\n');
      sections.set(part.slice(0, nl).trim(), part.slice(nl));
    }
    for (const n of allNotices()) {
      const body = sections.get(n.name);
      expect(body, `no "${n.name}" section in THIRD_PARTY_NOTICES.md`).toBeDefined();
      expect(body).toContain(`- Licence: ${n.licence}`);
      expect(body).toContain(`- ${n.notice}`);
      expect(body).toContain(`- Source: ${n.url}`);
      expect(body).toContain(`- Used for: ${n.role}`);
      if (n.terms) expect(body).toContain(n.terms);
    }
    expect(sections.size).toBe(allNotices().length + 3); // + the licence-text headings
    for (const para of MIT_LICENCE) {
      expect(md.replace(/\s*\n\s*/g, ' ')).toContain(para);
    }
  });

  it('name who each licence text covers', () => {
    expect(usedUnder('MIT')).toEqual([
      'eventemitter3',
      'Svelte',
      'SvelteKit',
      'esm-env',
      'Vite (module preload helper)',
    ]);
    expect(usedUnder('Apache-2.0')).toEqual(['hls.js', 'url-toolkit', 'videojs-contrib-hls']);
  });
});
