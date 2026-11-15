// Images from other hosts (TMDB posters and poster candidates, Live TV
// channel logos from M3U / XMLTV sources) load with
// referrerpolicy="no-referrer": with the server's strict-origin-when-cross-origin
// policy every such request would otherwise tell that host this server's
// address. Our own /artwork and asset URLs don't need it.
const sources = import.meta.glob<string>(['/src/routes/**/*.svelte', '/src/lib/components/**/*.svelte'], {
  query: '?raw',
  import: 'default',
  eager: true,
});

// src expressions that are third-party URLs as the API returns them.
const THIRD_PARTY_SRC = /src=\{[^}]*(poster_url|logo_url|channel_logo|selectedPoster|\bp\.url)\b[^}]*\}/;

describe('third-party images', () => {
  it('are found (the scan sees the templates)', () => {
    const hits = Object.values(sources).flatMap((src) => src.match(/<img\b[\s\S]*?>/g) ?? []).filter((t) => THIRD_PARTY_SRC.test(t));
    expect(hits.length).toBeGreaterThan(10);
  });

  it('never send this server as the referrer', () => {
    const missing: string[] = [];
    for (const [path, src] of Object.entries(sources)) {
      for (const tag of src.match(/<img\b[\s\S]*?>/g) ?? []) {
        if (THIRD_PARTY_SRC.test(tag) && !/referrerpolicy="no-referrer"/.test(tag)) missing.push(`${path}: ${tag.replace(/\s+/g, ' ')}`);
      }
    }
    expect(missing).toEqual([]);
  });
});
