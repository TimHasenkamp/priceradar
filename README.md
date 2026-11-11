# PriceRadar

Self-hosted price tracker for otto.de. It checks product prices at a fixed interval, stores the history in SQLite and shows it in a web UI: a dashboard plus a price chart for each product.

```
┌──────────┐  /api/* (Proxy)  ┌──────────────┐  POST /fetch  ┌───────────────────┐
│ web      │ ───────────────▶ │ backend (Go) │ ────────────▶ │ fetcher (Python)  │──▶ otto.de
│ SvelteKit│                  │ API+Scheduler│ ◀──── HTML ── │ Scrapling stealth │
└──────────┘                  └──────┬───────┘               └───────────────────┘
                                  SQLite (Volume)
```

| Service | Role |
|---|---|
| `web` (SvelteKit, port 3000) | UI. Forwards `/api/*` to the backend, so only one port is exposed |
| `backend` (Go, port 8080 internal) | REST API ([docs/API.md](docs/API.md)), serial scrape queue, parser, SQLite |
| `fetcher` (Python, port 8000 internal) | Renders pages in Scrapling's stealth browser (Patchright/Chromium) and returns HTML only. Contains no shop logic |

> **Note on otto.de:** otto.de uses the Kasada bot protection. Plain HTTP requests get a JS challenge (HTTP 400). That is why the sidecar uses a stealth browser. This bypasses the bot protection and may violate otto.de's terms of use; that risk is yours. robots.txt allows product pages (`/p/`) and is checked before every request. Requests run strictly one after another, with 15 s + jitter between them by default. If otto.de blocks requests anyway, the product shows a "Blockiert (Bot-Schutz)" badge in the UI.

## Quick start (Docker)

You only need [`docker-compose.yml`](docker-compose.yml) and optionally [`.env.example`](.env.example). The images are public on GitHub Container Registry:

```bash
curl -O https://raw.githubusercontent.com/TimHasenkamp/priceradar/main/docker-compose.yml
curl -o .env https://raw.githubusercontent.com/TimHasenkamp/priceradar/main/.env.example   # adjust as needed
docker compose pull
docker compose up -d
# → http://localhost:3000
```

| Image | Platforms |
|---|---|
| `ghcr.io/timhasenkamp/priceradar-web` | arm64 |
| `ghcr.io/timhasenkamp/priceradar-backend` | arm64 |
| `ghcr.io/timhasenkamp/priceradar-fetcher` | arm64, amd64 |

Web and backend are published for **arm64 only** (e.g. Raspberry Pi, Ampere/Graviton, Apple Silicon); the fetcher also for amd64 so it can run on a home PC. On amd64 servers, build locally with `docker-compose.build.yml`.

- **Tags:** `latest` (current `main`), `X.Y.Z` / `X.Y` (releases from git tags `vX.Y.Z`), `sha-<commit>`. Pin a version with `PRICERADAR_TAG=0.1.0` in `.env`.
- **Updating:** `docker compose pull && docker compose up -d`.
- **Storage:** the database lives in the `priceradar-data` volume. The fetcher image is large, about 1–1.5 GB, because it contains a browser.
- **Building from source instead:** `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.
- **Releases:** `git tag v0.1.0 && git push --tags` runs CI, which builds and pushes all three images ([.github/workflows/ci.yml](.github/workflows/ci.yml)).

## Fetcher on a different machine (residential IP)

otto.de's bot protection (Kasada) largely blocks **data-center IPs** such as Hetzner, AWS or Oracle, however good the browser's camouflage. If the backend runs at a hoster, run the fetcher on a machine at home and connect both over a private VPN such as NetBird, Tailscale or WireGuard:

```
[hoster]  web + backend  ──VPN──▶  [home] fetcher ──▶ otto.de (residential IP)
```

1. **Home machine:** the fetcher image is available for amd64 and arm64. Generate a secret with `openssl rand -hex 32`, then start the fetcher bound to the VPN IP only:
   ```bash
   FETCHER_TOKEN=<secret> FETCHER_BIND=<VPN IP of this machine>      docker compose -f docker-compose.fetcher.yml up -d
   ```
2. **Backend host:** remove the `fetcher` service and the backend's `depends_on`, then set:
   ```
   PR_FETCHER_URL=http://<VPN IP of the home machine>:8000
   PR_FETCHER_TOKEN=<same secret>
   ```
3. **VPN rules:** allow only backend host → home machine on TCP 8000.

If the home machine is off, checks fail as `transient` and are retried in the next cycle.

## Local development

Requirements: Go 1.26+, Node 24+, Python 3.12+ with [uv](https://docs.astral.sh/uv/).

```bash
# 1. Fetcher sidecar (one-time setup: venv + browser download)
cd fetcher
uv venv && uv pip install -r pyproject.toml && uv run scrapling install
FETCHER_ADDR=127.0.0.1 uv run main.py              # :8000

# 2. Scraper CLI for a quick test
go run ./cmd/scrape https://www.otto.de/p/bambu-lab-3d-drucker-a1-combo-CS00EO0AU/
go run ./cmd/scrape -json <url>

# 3. Backend
LOG_FORMAT=text go run ./cmd/server                # :8080, DB: ./priceradar.db

# 4. Frontend
cd web && npm install && npm run dev               # :5173, BACKEND_URL defaults to http://localhost:8080
# without backend/fetcher: npm run mock (fake API on :8080)
```

Tests (none of them make live requests; the parser tests use saved HTML from `testdata/otto/`):

```bash
go test -race -cover ./...
cd web && npm run check && npx vitest run
```

## Configuration

All settings are environment variables; see [.env.example](.env.example).

| Variable | Default | Meaning |
|---|---|---|
| `PR_SCRAPE_INTERVAL` | `6h` | Interval of the periodic run. After a restart, products checked less than half an interval ago are skipped |
| `PR_REQUEST_DELAY` / `PR_REQUEST_JITTER` | `15s` / `10s` | Pause between two requests to the same host |
| `PR_MAX_RETRIES` / `PR_RETRY_BACKOFF` | `3` / `30s` | Retries for `transient`/`blocked` errors in the periodic run, waiting ×4 longer each time. Manual refreshes make one attempt only |
| `PR_FETCH_TIMEOUT` | `60s` | Browser timeout per page |
| `PR_USER_AGENT` | `PriceRadar/0.1 (…)` | User agent for robots.txt |
| `PR_DB_PATH` | `priceradar.db` | SQLite file |
| `PR_LISTEN_ADDR` | `:8080` | Backend address |
| `PR_FETCHER_URL` | `http://localhost:8000` | Fetcher sidecar |
| `PR_FETCHER_TOKEN` | – | Shared secret, sent as `Authorization: Bearer …`. Must match `FETCHER_TOKEN` of the fetcher |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | slog output |

## How a price is read (otto.de)

1. **JSON-LD** (`<script type="application/ld+json">`, schema.org `Product` → `offers`): `price`, `priceCurrency`, `availability`, plus `name`, `image` and `sku`.
2. **Price block** `div.js_pdp_price > ofc-pricing-item-v1`: the attributes `old-price` (strike price) and `suggested-retail-price` (RRP, "UVP"). These are missing from JSON-LD. If JSON-LD is missing, `retail-price` / `data-price-cents` serves as a fallback, and that check is logged as a warning.

Only the **first** price block counts. Recommendations further down the page use similar markup.

**Kasada challenge:** otto.de often answers even the stealth browser first with a JS challenge (HTTP 400). The browser solves it and reloads the page. The sidecar therefore waits for `script[type="application/ld+json"]` (`otto.WaitSelector`, passed as `wait_selector`) before it returns the HTML. Real product pages also load the Kasada SDK (`KPSDK`), so the parser only treats a page as `blocked` if it has `KPSDK` and no title or product data.

Every check is stored, failed ones included, with an error kind:

| Kind | Meaning | Retry? |
|---|---|---|
| `blocked` | Bot protection (Kasada challenge, HTTP 400/403/429) | yes |
| `layout_changed` | Neither JSON-LD nor price block found, so the parser probably needs adjusting | no |
| `not_found` | HTTP 404/410 | no |
| `transient` | Timeout, 5xx, sidecar unreachable | yes |
| `robots_disallowed` | robots.txt forbids the URL | no |

From 2 consecutive failures, the dashboard card shows a warning badge with the error kind.

## Adding a new shop

1. **Create a package** `internal/scraper/<shop>/` and implement the `scraper.Scraper` interface:

   ```go
   type Scraper interface {
       Name() string                                         // stable ID, stored with the product, e.g. "mediamarkt"
       Supports(u *url.URL) bool                             // is this a product URL of this shop?
       Fetch(ctx context.Context, url string) (Offer, error) // download + parse
   }
   ```

   Optionally also implement `NormalizeURL(*url.URL) string` to strip tracking parameters. That is how duplicates are detected.

2. **Return errors as sentinels:** wrap `scraper.ErrBlocked`, `ErrLayoutChanged`, `ErrNotFound` or `ErrTransient` with `fmt.Errorf("...: %w", scraper.ErrX)`. Retries, error badges and logs rely on them.

3. **Choose the fetcher:** use `scraper.HTMLFetcher` and wrap it in `scraper.NewPolite(...)` for pauses and robots.txt.
   - Shops **without** JS bot protection: a small `HTMLFetcher` around `net/http` is enough.
   - Shops **with** JS bot protection: use the sidecar (`scraper.SidecarFetcher`) and add the host to `FETCHER_ALLOWED_HOSTS`.

4. **Parser tests:** save 2–3 real product pages (normal, reduced, sold out) to `testdata/<shop>/`. Write a table-driven test against them, like [internal/scraper/otto/parse_test.go](internal/scraper/otto/parse_test.go). Tests must not make live requests.

5. **Register** the scraper in [cmd/server/main.go](cmd/server/main.go) and [cmd/scrape/main.go](cmd/scrape/main.go):

   ```go
   reg := scraper.NewRegistry(otto.New(polite), myshop.New(politeHTTP))
   ```

   The frontend needs no changes.

## Project structure

```
cmd/scrape/           CLI: fetch a single URL
cmd/server/           API + scheduler
internal/config/      env parsing
internal/scraper/     interface, registry, sidecar client, polite fetcher, robots.txt, retry
internal/scraper/otto otto.de parser
internal/scheduler/   serial scrape queue (periodic run + manual jobs)
internal/store/       SQLite, versioned migrations (internal/store/migrations/*.sql)
internal/api/         REST handlers
fetcher/              Python sidecar (Scrapling)
web/                  SvelteKit frontend
testdata/otto/        HTML fixtures (trimmed with scripts/trim_fixture.py)
docs/API.md           API contract
```

The fixtures are real otto.de pages, cut down to title, JSON-LD (product descriptions removed) and price block, with Kasada tokens redacted. Trim new fixtures the same way: `python scripts/trim_fixture.py page.html testdata/otto/xyz.html`.

## License

[MIT](LICENSE). Using the software against a shop's terms of use is your own responsibility.
