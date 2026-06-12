# Stream Dashboard for Silo

`silo.stream-dashboard` is an operator dashboard for Silo stream activity. It serves an embedded React SPA that surfaces active sessions, playback history, a world map view, and a globe view of where streams are originating, backed by a plugin-owned Postgres schema that mirrors data from the Silo host database.

## Category

Lives under **Operations** alongside [`silo-plugin-notifications`](https://github.com/RXWatcher/silo-plugin-notifications) (the cross-plugin event hub) and [`silo-plugin-support`](https://github.com/RXWatcher/silo-plugin-support) (the customer-support shell).

## Capabilities

| Type | ID | Purpose |
| --- | --- | --- |
| `http_routes.v1` | `dashboard` | Serves the admin SPA and a JSON API for active sessions, playback history, counts, map points, and overview/health under `/admin`, `/dashboard`, and `/api/*`. Also publishes a navigable admin entry labelled "Stream Dashboard". |
| `scheduled_task.v1` | `sync-playback-history` | Copies playback history from the Silo source database into the plugin schema and applies the configured retention policy. |

## Dependencies

- Silo plugin host (gRPC plugin runtime from [`continuum-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk)).
- Read-only access to the Silo host's Postgres database (active sessions, playback history). The plugin never writes back to the Silo schema; it only reads.

This plugin is otherwise standalone — it does not depend on other plugins.

Host: [`Silo-Server/silo-server`](https://github.com/Silo-Server/silo-server).

## External services

- **Postgres** (required, two DSNs):
  - `plugin_database` — owned by the plugin, holds the `stream_dashboard` schema with synced history, cached map data, and the persisted app config. Migrations run automatically on startup.
  - `silo_database` — read-only DSN for Silo's public schema. Used to pull active sessions and playback history.
- **GeoIP** (optional). When enrichment is enabled the plugin can resolve client and CDN node IPs to coordinates via:
  - A local MaxMind-format MMDB file (`oschwald/geoip2-golang`).
  - HTTP providers: `ip-api.com`, `ipinfo.io`, `ipwho.is`. Provider order, per-provider toggles, base URLs, and an ipinfo token are configurable, with an in-memory TTL cache in front.

## Dashboard views

The SPA (under `web/`, embedded into the binary via `go:embed`) renders four panels driven by a single `/api/overview` poll plus targeted endpoints:

- **Active sessions** — live list of currently-playing sessions with user, media, transcode state, device, codecs, IP, and host VM metrics. Polled on the configured `refresh_seconds` interval (default 30s, minimum 5s); there is no websocket.
- **Playback history** — paginated history rows synced from the Silo host, filtered by the active retention policy.
- **World map** — 2D map (`WorldMap.tsx` + `MapLegend.tsx`) plotting session origins by resolved coordinates.
- **Globe view** — 3D globe (`GlobeView.tsx`) of the same session points for an at-a-glance geographic overview.

A status/health banner reports per-section failures (counts, sessions, map, history) so partial outages stay visible without breaking the rest of the dashboard.

## Configuration

Two global config keys are required, plus an optional `stream_dashboard` block for behavior tuning:

| Key | Required | Purpose |
| --- | --- | --- |
| `plugin_database.database_url` | yes | Postgres DSN for the plugin-owned `stream_dashboard` schema. |
| `silo_database.database_url` | yes | Read-only Postgres DSN for the Silo host's public schema. |
| `stream_dashboard.refresh_seconds` | no | Dashboard polling interval. Default `30`, floor `5`. |
| `stream_dashboard.default_server_lat` / `default_server_lon` | no | Fallback coordinates for sessions with no resolvable IP. |
| `stream_dashboard.history_retention_days` | no | Default `365`. `0` disables the day-based prune. |
| `stream_dashboard.history_retention_max_rows` | no | Row cap; `0` disables. |
| `stream_dashboard.history_retention_min_seconds` | no | Drop rows with less than N watched seconds. |
| `stream_dashboard.history_retention_completed_only` | no | Retain only completed plays. |
| `stream_dashboard.geoip_enabled` | no | Master switch for GeoIP enrichment. |
| `stream_dashboard.geoip_database_path` | no | Path to a local MMDB file. |
| `stream_dashboard.geoip_lookup_missing_coordinates` | no | Resolve sessions that arrive without lat/lon. Default `true`. |
| `stream_dashboard.geoip_override_session_coordinates` | no | Replace existing coordinates with GeoIP results. |
| `stream_dashboard.geoip_lookup_cdn_nodes` | no | Also resolve CDN node IPs. Default `true`. |
| `stream_dashboard.geoip_include_private_ips` | no | Resolve RFC1918 addresses too. |
| `stream_dashboard.geoip_provider_order` | no | Ordered list, default `mmdb,ipapi,ipinfo,ipwhois`. |
| `stream_dashboard.geoip_request_timeout_seconds` | no | Per-provider HTTP timeout. Floor `1`, default `3`. |
| `stream_dashboard.geoip_cache_ttl_seconds` | no | Resolution cache TTL. Floor `60`, default `3600`. |
| `stream_dashboard.geoip_cache_max_entries` | no | Cache size cap. Floor `100`, default `4096`. |

### Retention

The `sync-playback-history` scheduled task pulls new rows from the Silo source DB into the plugin schema, then applies `RetentionPolicy{Days, MaxRows, MinWatchSeconds, CompletedOnly}` to prune anything outside the policy. The dashboard reports per-run `synced_rows` and `pruned_rows` so retention behavior is observable from the SPA.

## Detailed docs

- [`docs/setup-debug-flows.md`](docs/setup-debug-flows.md) — operator runbook: health checks, two-DSN setup, history sync, config lifecycle, common failure patterns, routes reference.
- [`docs/architecture.md`](docs/architecture.md) — module layout, data flow, scheduled vs realtime sync, health banner wiring.
- [`docs/geoip.md`](docs/geoip.md) — provider model, MMDB vs HTTP tradeoffs, TTL cache, private-IP handling, source-field reference.

## Build and release

```bash
make build   # builds the web SPA via pnpm, then compiles the Go binary
make test    # runs `go test ./...` and the Vitest suite under web/
```

CI builds linux-amd64 binaries on push to main via the reusable workflow in [RXWatcher/silo-plugin-repository](https://github.com/RXWatcher/silo-plugin-repository) and publishes them to the catalog at [`./binaries/`](https://github.com/RXWatcher/silo-plugin-repository/tree/main/binaries).
