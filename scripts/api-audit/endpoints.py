import os, re, sys, json

ROOT = sys.argv[1]
OUT = sys.argv[2]
CLIENTS = {
    'web':         ['web/src'],
    'androidTV':   ['clients/android/app/src/main'],
    'phone+quest': ['clients/android_native/app/src/main'],
    'webos':       ['clients/webos/src'],
    'tizen':       ['clients/tizen/src'],
    'xbox':        ['clients/xbox/src', 'clients/xbox/shell'],
    'roku':        ['clients/roku'],
    'desktop':     ['clients/desktop'],
    'server-self': ['internal', 'cmd'],   # worker, cast, OPDS links built server-side
    'docs':        ['docs', 'README.md', 'ARCHITECTURE.md'],
}
EXT = ('.ts', '.svelte', '.js', '.kt', '.java', '.brs', '.xml', '.rs', '.go', '.md', '.cs', '.html', '.json', '.yaml')
SKIP_DIR = re.compile(r'(node_modules|/build/|/dist/|\.svelte-kit|/target/|/gen/|/bin/|/obj/|AppPackages|/test/|/tests/|__tests__|/androidTest/)')
SKIP_FILE = re.compile(r'(_test\.go|\.test\.ts|\.spec\.ts|Test\.kt|_test\.brs)$')


def norm(p):
    return p.replace(os.sep, '/')


def files(paths, client):
    for p in paths:
        full = os.path.join(ROOT, p)
        if os.path.isfile(full):
            yield full
            continue
        for dp, dn, fn in os.walk(full):
            d = norm(dp) + '/'
            if SKIP_DIR.search(d):
                continue
            for f in fn:
                if not f.endswith(EXT) or SKIP_FILE.search(f):
                    continue
                # A route must not count as using itself: skip the router and
                # the API handler packages when scanning the server.
                if client == 'server-self' and (f.endswith('.md') or '/internal/api/' in d):
                    continue
                yield os.path.join(dp, f)


text = {}
for c, ps in CLIENTS.items():
    buf = []
    for f in files(ps, c):
        try:
            buf.append(open(f, encoding='utf-8', errors='ignore').read())
        except OSError:
            pass
    text[c] = '\n'.join(buf)

PARAM = (r'(?:\$\{[^}]{1,80}\}'            # TS / Kotlin ${expr}
         r'|\$[A-Za-z_][\w.]*'              # Kotlin $var
         r'|\{[A-Za-z_]\w*\}'               # Retrofit {id}
         r'|["\']\s*\+\s*[^"\'\n]{1,60}?\s*\+\s*["\']'   # "..." + x + "..." (TS, BrightScript, Java)
         r'|%[sdv]'                          # Go / Kotlin format
         r'|[A-Za-z0-9_.\-]+)')             # a literal id in docs / tests


def rx(route):
    path = route.split(' ', 1)[1]
    if path.endswith('/*'):
        path = path[:-2]
    segs = [s for s in path.strip('/').split('/') if s]
    if segs[:2] == ['api', 'v1']:
        segs = segs[2:]
        pre = r'(?:/?api/v1)?/'
    else:
        pre = r'/?'
    parts = []
    for s in segs:
        if s.startswith('{') and s.endswith('}'):
            parts.append(PARAM)
        elif '{' in s:
            parts.append(re.escape(s.split('{')[0]) + PARAM)
        else:
            parts.append(re.escape(s))
    return re.compile(pre + '/'.join(parts) + r'(?![A-Za-z0-9_\-])')


routes = [l.strip() for l in open(os.path.join(OUT, 'routes.txt'))
          if l.strip() and not l.startswith(('OPTIONS', 'HEAD'))]
cols = list(CLIENTS)
rows = []
for r in routes:
    pat = rx(r)
    rows.append((r, {c: len(pat.findall(t)) for c, t in text.items()}))

with open(os.path.join(OUT, 'endpoint_matrix.tsv'), 'w', encoding='utf-8') as o:
    o.write('route\t' + '\t'.join(cols) + '\n')
    for r, h in rows:
        o.write(r + '\t' + '\t'.join(str(h[c]) for c in cols) + '\n')

apps = [c for c in cols if c not in ('server-self', 'docs')]
none = [r for r, h in rows if all(h[c] == 0 for c in cols)]
docs_only = [r for r, h in rows if all(h[c] == 0 for c in apps) and h['server-self'] == 0 and h['docs'] > 0]
self_only = [r for r, h in rows if all(h[c] == 0 for c in apps) and h['server-self'] > 0]
print(f'routes checked: {len(rows)}  no reference anywhere: {len(none)}  docs only: {len(docs_only)}  server-self only: {len(self_only)}')
json.dump({'none': none, 'docs_only': docs_only, 'self_only': self_only},
          open(os.path.join(OUT, 'candidates.json'), 'w'), indent=1)
