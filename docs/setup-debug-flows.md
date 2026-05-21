# Stream Dashboard Operations And Debugging

Operator runbook for `continuum.stream-dashboard`. Pairs with the top-level [`README.md`](../README.md) (capabilities, config schema, build) and the deeper notes in [`architecture.md`](architecture.md) and [`geoip.md`](geoip.md). This file focuses on running, observing, and fixing the plugin in production.

This plugin is **admin only**. Every route except `/assets/*` requires `X-Continuum-User-Role: admin` (or the legacy `X-Continuum-Role` header). End users never see the dashboard.

## Quick Health Check

When something looks off, in this order:

1. Continuum Admin -> Plugins -> `continuum.stream-dashboard` shows the install as **enabled** and the binary version matches the latest catalog release.
2. Open `/admin` (the navigable entry "Stream Dashboard") through Continuum. Hit `GET /api/status` directly to confirm the plugin process responds — it returns `database_time` (proves plugin DB reachable), `sessions` count (proves source DB reachable), `servers` count, and `refresh_seconds`.
3. Look at the per-section health banner in the SPA. Each panel has its own health cell driven by `/api/overview`:

   | Cell | Code keys | What "not OK" means |
   | --- | --- | --- |
   | `counts` | `counts_failed` | Plugin DB or source DB query for `stream_nodes` / `playback_sessions_sync` / local `playback_history` failed. |
   | `sessions` | `sessions_failed` | The source DB query against `public.playback_sessions_sync` failed. Sessions panel is empty/stale. |
   | `map` | `map_failed` | Source DB query failed OR GeoIP failed for every row — see `geoip.md`. |
   | `history` | `history_failed` | Plugin DB read of `playback_history` failed. Does **not** mean sync failed (sync is read-then-write and runs in `/api/overview`'s read-only path only via the cron). |

   A single failing cell leaves the rest of the dashboard alive — that is by design. Treat partial health as the diagnostic, not as an outage.

4. Plugin process logs (`hclog`, name `continuum-plugin-stream-dashboard`) — look for `configured stream-dashboard plugin` on startup, and for `migrate plugin schema`, `connect continuum source database`, `open geoip database` errors during reconfigure.

## Two-DSN Setup

The plugin always wants **two** DSNs. They are different roles with different permissions:

| Config key | Pool | Role | What it does |
| --- | --- | --- | --- |
| `plugin_database.database_url` | `pool` | Read/write, owned by the plugin | Holds `playback_history`, `sync_state`, `app_config`. Migrations run on every `Configure` call. |
| `continuum_database.database_url` | `sourcePool` | **Read-only** SELECT on `public.*` | Source of active sessions and the history feed. Plugin never writes here. |

If only one is provided, `cmd/.../main.go` falls back to using the plugin DSN for both — this is fine for dev against a host DB but **never** what you want in production because plugin schema would land in the Continuum public schema namespace.

Tables the plugin reads from the source DB:

- `public.playback_sessions_sync` — live sessions (joined with `users`, `media_files`, `media_items`, `episodes`)
- `public.playback_history_admin` — append-only history feed used by the cron
- `public.stream_nodes` — server inventory for the counts panel
- `public.users`, `public.media_items`, `public.episodes`, `public.media_files` — joins for friendly names

Grant the role used in `continuum_database.database_url` only `SELECT` on those tables. The plugin will not attempt any writes.

Tables in the plugin schema (created by `store.Migrate`):

- `playback_history` (PK `session_id`, indexed on `ended_at DESC`, `(user_id, ended_at DESC)`, `media_type`)
- `sync_state` — KV with cursor keys `history_cursor_ended_at`, `history_cursor_session_id`, `history_last_sync_at`
- `app_config` — singleton `id=1` JSONB row mirroring `pluginrt.Config` (excluding the two DSNs, which are zeroed on persist)

## Playback History Sync

The `scheduled_task.v1` capability `sync-playback-history` is invoked by the host scheduler. Each `Run` call:

1. Reads the cursor `(history_cursor_ended_at, history_cursor_session_id)` from `sync_state`. If unset, seeds from `MAX(ended_at)` in local `playback_history`.
2. Rewinds the query cursor by **2 minutes** (`historyCursorOverlap`) to absorb late inserts; the `ON CONFLICT (session_id) DO NOTHING` upsert deduplicates.
3. Pages up to **20 batches** of **5,000 rows** per run (`historyScheduledMaxBatches * historyBatchLimit` = up to 100k rows/run). The realtime path (used inline by `PlaybackHistory`, not by the cron) is gated to 1 batch and throttled to once every 10 s via `historyRealtimeSyncInterval`.
4. Writes `history_last_sync_at` to `sync_state`.
5. Applies the `RetentionPolicy` in this strict order:
   - `MinWatchSeconds > 0` -> delete rows with `watched_seconds < N`
   - `CompletedOnly == true` -> delete rows with `completed = false`
   - `Days > 0` -> delete rows older than `NOW() - Days::interval`
   - `MaxRows > 0` -> keep newest `MaxRows` (by `ended_at DESC`), delete the rest

   Set any field to `0` (or `false` for `CompletedOnly`) to disable that rule. The defaults (`Days=365`, others zero) keep a year of everything.

The SPA shows `synced_rows` and `pruned_rows` per run on the history panel header — that comes from the response of the inline realtime sync path, not the cron. The cron writes nothing to `/api/overview`.

### History not syncing

Symptom: `history.total` in `/api/counts` stays flat, `last_sync_at` is old.

1. Check Continuum scheduled task logs for `sync-playback-history`. The plugin returns `nil` if `store` or `policy` is nil (means the plugin failed `Configure` — see logs).
2. Confirm the source DB user can `SELECT` `public.playback_history_admin`. The plugin masks the table; if the host renamed it, sync fails silently per batch.
3. Inspect cursor state directly: `SELECT * FROM sync_state WHERE key LIKE 'history_%'`. If `history_cursor_ended_at` is in the future, the cursor is stuck — `DELETE FROM sync_state WHERE key LIKE 'history_cursor_%'` will reseed from `MAX(ended_at)` next run.
4. If retention is mis-tuned and rows are being pruned as fast as they sync (e.g. `MinWatchSeconds` too high), `pruned_rows` will match `synced_rows` per run — visible in the SPA history header.

### History panel is empty but counts show rows

The history list (`/api/history`) reads `playback_history` directly. If `Total > 0` but `Items` is empty, the `LIMIT/OFFSET` paging is past the end — reset to page 1.

## Active Sessions And the 30 s Cadence

There is **no websocket**. The SPA polls `/api/overview` every `refresh_seconds` (default 30, floor 5 enforced both in `NormalizeAppConfig` and in `refreshSeconds()` in the server). The server returns:

```json
{"counts": {...}, "sessions": [...], "map_sessions": [...], "history": {...}, "refresh_seconds": 30, "generated_at": "...", "health": {...}}
```

If you want a faster pulse for debugging, set `stream_dashboard.refresh_seconds` to 5. Going below that has no effect — the floor kicks in.

### Sessions stuck "active"

`Sessions()` reads `public.playback_sessions_sync` directly — there is no plugin-side cleanup. If sessions linger after a client disconnect, that is the host's `playback_sessions_sync` table not being cleaned up by Continuum. The plugin will faithfully report whatever the source says. Confirm by querying source DB directly:

```sql
SELECT session_id, started_at, updated_at, is_paused FROM public.playback_sessions_sync;
```

If those rows are stale in the source DB, escalate to the Continuum host — not a plugin bug.

## GeoIP And the Map

See [`geoip.md`](geoip.md) for the full provider model. Quick triage:

- **No dots on map** but sessions present: every session lacked coordinates **and** GeoIP enrichment failed or is disabled. `MapSessions` only emits rows where `client.Lat != nil && client.Lon != nil` — sessions with no resolvable client IP are dropped from the map entirely, not rendered at `(0,0)`.
- **All dots clustered at one point**: that point is `default_server_lat` / `default_server_lon` (defaults `37.5485, -121.9886` — Fremont, CA). It's used for the **server** endpoint only (the line origin), never for clients. If clients cluster there, they are NOT falling back — re-check GeoIP responses.
- **Wrong locations**: provider order is `mmdb, ipapi, ipinfo, ipwhois` by default. First successful response wins (`Source: "geoip:<name>"`). Reorder via `geoip_provider_order` to prefer one source. The TTL cache (`geoip_cache_ttl_seconds`, default 3600, floor 60) will hold a wrong answer until expiry — restart the plugin to flush.
- **Private IPs not resolved**: by design. Set `geoip_include_private_ips=true` to resolve RFC1918 / loopback. The MMDB provider will typically return 0,0 for those anyway.

CDN node enrichment is independent: `geoip_lookup_cdn_nodes` (default true) controls whether `session.cdn_node_ip` is resolved. The map line is drawn client -> CDN -> server when both endpoints have coordinates.

## SPA + Binary Pairing

The Go binary embeds the built SPA via `go:embed` (`web/embed.go` -> `web/dist/`). `make build` runs the pnpm build first, then `go build`. A binary published without a fresh `pnpm build` will serve a stale SPA — visible as missing UI features even though `/api/*` works.

Asset paths are rewritten per request in `rewritePluginAssets`:

- `/admin`, `/dashboard`, `/` -> `assets/...` (relative)
- `/admin/something`, `/dashboard/something` -> `../assets/...` (relative, one level up)

If you reverse-proxy the SPA but not `/assets/*`, the SPA loads the HTML shell with no JS. Make sure the proxy forwards all four: `/api/*`, `/assets/*`, `/admin*`, `/dashboard*`.

Theme is injected from `X-Continuum-Theme` (or `?theme=` for browser testing) into `<html data-theme="...">`. No theme header is fine; the SPA picks its own default.

## Configuration Lifecycle

`Configure` is called by the host on install, reconfigure, and restart:

1. The two DSNs come in as `plugin_database` and `continuum_database` global config blocks. Either `database_url` or `value` or the first string in the map is accepted.
2. `pluginrt.Config` is built with defaults, then overlaid with `stream_dashboard.*` keys.
3. Plugin DB is connected, migrated, and the **persisted** `app_config` row is loaded via `ImportLegacyAppConfig`. If `app_config` already differs from `DefaultAppConfig()`, the persisted version **wins** over the global config — meaning the SPA's "Save" via `PATCH /api/config` is sticky across reconfigures.
4. Source DB is connected and pinged.
5. GeoIP locator is opened if `geoip_enabled=true`. Failing here aborts `Configure` and the previous pools are NOT swapped in — the plugin keeps serving with old config.
6. On success, old pools and locator are closed via `atomic.Pointer.Swap`. There is no live reload; everything is rebuilt fresh.

This is why config changes need a reconfigure (host-driven) — there's no `SIGHUP`.

### Two configs, one source of truth

A subtle gotcha: the global config from the host and `app_config` in the plugin DB can drift. The plugin DB version wins after first save. If you change a value through Continuum Admin and don't see it apply, check `SELECT data FROM app_config WHERE id = 1` — if the field already has a value there, the host change is ignored except for the two DSNs (always taken from the global config) and `RefreshSeconds` / retention / GeoIP toggles read from the merged `cfg`.

To force a reset of persisted config: `UPDATE app_config SET data = '{}'::jsonb WHERE id = 1;` then reconfigure.

## Common Failure Patterns

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `/api/status` returns `not_configured` 503 | `Configure` never succeeded; pools were never created. | Check plugin process logs for the actual error from `connect server-manager database`, `migrate plugin schema`, `connect continuum source database`, or `open geoip database`. |
| `/api/status` returns 502 `status_failed` | Plugin DB reachable but query (`SELECT NOW()` or counts) failed. | Verify schema migrated cleanly — `\dt` should show `playback_history`, `sync_state`, `app_config`. |
| All API routes return 403 `forbidden` | Auth header missing or wrong. | Continuum host must set `X-Continuum-User-Role: admin`. The plugin also accepts `X-Continuum-Role`. Public assets at `/assets/*` are exempt. |
| Map empty, sessions populated | GeoIP off, or every client IP is private. | Enable `geoip_enabled` and at least one provider, or set `geoip_include_private_ips`. |
| `cdn` field always missing on map | `geoip_lookup_cdn_nodes=false` or `session.cdn_node_ip` empty in source. | The plugin's current source query hardcodes empty for CDN — CDN entries only appear if the source DB schema later adds the field. |
| Plugin DB grows unbounded | Retention is permissive (default 365 days, no cap). | Set `history_retention_max_rows` and/or shorten `history_retention_days`. |
| Server map points all at Fremont, CA | Default `default_server_lat`/`default_server_lon` not overridden. | Set both keys to your actual server location. The defaults are placeholder coords. |
| New SPA features missing after deploy | Binary built without rebuilding `web/dist/`. | Always `make build` (it runs pnpm build first). CI does this; manual builds must too. |
| Reconfigure didn't change behavior | `app_config` in plugin DB shadowing global config. | Inspect `app_config.data`; reset to `'{}'::jsonb` if needed. |

## Verifying A Change

1. Trigger reconfigure (toggle install in Continuum Admin or use the host's reload).
2. Confirm logs show `configured stream-dashboard plugin` and no errors above.
3. Hit `GET /api/status` — `database_time`, non-zero `sessions` (if any), and the new `refresh_seconds` echo back.
4. Hit `GET /api/overview` once and check the `health` map — all four cells `{ok: true}`.
5. For history work, run the `sync-playback-history` task on demand from Continuum, then re-check `last_sync_at` in `/api/history`.
6. For GeoIP work, pick a known public IP from sessions and check the map dot's `source` field via `/api/map` — `geoip:mmdb` vs `geoip:ipapi` etc. tells you which provider answered.

## Exposed Routes Reference

| Method | Path | Access | Notes |
| --- | --- | --- | --- |
| `*` | `/api/*` | admin | All JSON endpoints. Requires admin header. |
| GET | `/assets/*` | public | Static SPA assets. Served from embedded FS. |
| GET | `/dashboard`, `/dashboard/*` | admin | SPA shell. Same content as `/admin*`. |
| GET | `/admin` | admin | Navigable entry ("Stream Dashboard"). |
| GET | `/admin/*` | admin | SPA deep links. |

JSON endpoints under `/api`:

- `GET /status` — basic health + counts snapshot
- `GET /sessions` — `Session[]`, all current rows
- `GET /history?page=N&limit=M` — paginated `PlaybackHistoryPage` (limit 1-500, page 1-10000)
- `GET /counts` — full `Counts` struct
- `GET /map` — `MapSession[]` (only rows with resolved client coords)
- `GET /overview` — single-shot composite used by the SPA's main poll
- `GET /config` — current `app_config` (DSNs zeroed)
- `PATCH /config` — partial update, normalized and persisted

Query params on history support overriding retention per-request (`history_days`, `history_rows`, `history_min_seconds`, `history_completed_only=true`) but only when calling `PlaybackHistory` (realtime) — the SPA path uses `PlaybackHistoryReadOnly` and ignores these.
