# API audit

Finds `/api/v1` routes and JSON fields that no first-party client uses.
Results and decisions: [docs/v3-api-audit.md](../../docs/v3-api-audit.md).

From the repo root:

```sh
mkdir -p /tmp/apiaudit
ROUTES_OUT=/tmp/apiaudit/routes.txt go test ./internal/api/ -run TestDumpRoutes -count=1
python scripts/api-audit/endpoints.py . /tmp/apiaudit   # endpoint_matrix.tsv, candidates.json
python scripts/api-audit/fields.py . /tmp/apiaudit      # field_matrix.tsv, field_candidates.json
```

`endpoints.py` counts references per client (web, Android TV, phone + Quest,
webOS, Tizen, Xbox, Roku, desktop), plus the server itself and the docs. A
route with no client reference isn't necessarily dead: HLS segments, OPDS,
webhooks and email links are reached through URLs the server hands out.
Re-check every candidate by hand (the docs list how each one was settled).
