import os, re, sys, json, collections

ROOT = sys.argv[1]
OUT = sys.argv[2]
SRC = os.path.join(ROOT, 'internal', 'api', 'v1')
CLIENTS = {
    'web':         ['web/src'],
    'androidTV':   ['clients/android/app/src/main'],
    'phone+quest': ['clients/android_native/app/src/main'],
    'webos':       ['clients/webos/src'],
    'tizen':       ['clients/tizen/src'],
    'xbox':        ['clients/xbox/src', 'clients/xbox/shell'],
    'roku':        ['clients/roku'],
    'desktop':     ['clients/desktop/src', 'clients/desktop/src-tauri/src'],
}
EXT = ('.ts', '.svelte', '.js', '.kt', '.java', '.brs', '.xml', '.rs', '.cs')
SKIP_DIR = re.compile(r'(node_modules|/build/|/dist/|\.svelte-kit|/target/|/gen/|/bin/|/obj/|AppPackages|/test/|/tests/|__tests__|/androidTest/)')
SKIP_FILE = re.compile(r'(\.test\.ts|\.spec\.ts|Test\.kt|_test\.brs)$')

# 1. Every json tag in the v1 handlers, with the struct it belongs to.
TAG = re.compile(r'^\s*(\w+)\s+[^`\n]*`[^`]*json:"([a-zA-Z0-9_]+)[,"]', re.M)
STRUCT = re.compile(r'^type\s+(\w+)\s+struct\s*\{', re.M)
fields = collections.defaultdict(set)   # json name -> {Struct.Field}
for f in sorted(os.listdir(SRC)):
    if not f.endswith('.go') or f.endswith('_test.go'):
        continue
    src = open(os.path.join(SRC, f), encoding='utf-8').read()
    # split by struct declarations so each tag knows its struct
    marks = [(m.start(), m.group(1)) for m in STRUCT.finditer(src)]
    for m in TAG.finditer(src):
        owner = '?'
        for pos, name in marks:
            if pos < m.start():
                owner = name
        if m.group(2) == '-':
            continue
        fields[m.group(2)].add(f'{f}:{owner}.{m.group(1)}')

# 2. Client text.
def files(paths):
    for p in paths:
        full = os.path.join(ROOT, p)
        for dp, dn, fn in os.walk(full):
            if SKIP_DIR.search(dp.replace(os.sep, '/') + '/'):
                continue
            for x in fn:
                if x.endswith(EXT) and not SKIP_FILE.search(x):
                    yield os.path.join(dp, x)

text = {}
for c, ps in CLIENTS.items():
    text[c] = '\n'.join(open(f, encoding='utf-8', errors='ignore').read() for f in files(ps))

# A snake_case name is matched as a whole word; Kotlin/Moshi may map it to a
# camelCase property with @Json(name = "x") or @SerializedName("x"), which
# still contains the literal.
rows = []
for name in sorted(fields):
    pat = re.compile(r'(?<![A-Za-z0-9_])' + re.escape(name) + r'(?![A-Za-z0-9_])')
    hits = {c: len(pat.findall(t)) for c, t in text.items()}
    rows.append((name, hits, sorted(fields[name])))

unused = [(n, owners) for n, h, owners in rows if sum(h.values()) == 0]
one_client = [(n, [c for c in h if h[c]][0], owners) for n, h, owners in rows if sum(1 for c in h if h[c]) == 1]
with open(os.path.join(OUT, 'field_matrix.tsv'), 'w', encoding='utf-8') as o:
    o.write('field\t' + '\t'.join(CLIENTS) + '\towners\n')
    for n, h, owners in rows:
        o.write(n + '\t' + '\t'.join(str(h[c]) for c in CLIENTS) + '\t' + ' '.join(owners) + '\n')
json.dump({'unused': unused}, open(os.path.join(OUT, 'field_candidates.json'), 'w'), indent=1)
print(f'json field names in internal/api/v1: {len(rows)}; in no client: {len(unused)}; in exactly one client: {len(one_client)}')
