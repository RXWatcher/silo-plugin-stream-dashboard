# Architecture Notes

Operator-oriented view of how the Stream Dashboard plugin is wired together. Intended for someone debugging or extending the plugin — not a user-facing overview. See [`../README.md`](../README.md) for capabilities and config.

## Process Layout

The binary is a single Go process spawned by the Continuum plugin host over gRPC (`pluginsdk/runtime.Serve`). It registers three capability servers from `internal/`:

```
cmd/continuum-plugin-stream-dashboard/main.go
  -> pluginrt.Server (Runtime/Configure)         [internal/runtime]
  -> httproutes.Server (HTTPRoutes)              [internal/httproutes]
  -> poll.Server (ScheduledTask)                 [internal/poll]
```

`main.go` holds three `atomic.Pointer` slots: the plugin DB pool, the source DB pool, and the GeoIP locator. `Configure` builds a fresh trio and `Swap`s them in, closing the old set. The HTTP handler is rebuilt and swapped via `httpSrv.SetHandler(...)` on each reconfigure. There is no graceful drain — in-flight requests against the old handler keep their pool references via closure.

## Data Flow

```
                Continuum host DB (read-only)
                          |
                          | sourcePool (pgxpool)
                          v
                  internal/store/store.go
                  - Sessions()              -> public.playback_sessions_sync
                  - Counts()                -> stream_nodes + sessions + plugin history
                  - SyncPlaybackHistory()   -> public.playback_history_admin -> plugin DB
                          |
                          | pool (pgxpool, owned)
                          v
                Plugin DB schema (stream_dashboard)
                  - playback_history   (synced + retention-pruned)
                  - sync_state         (cursors, last_sync_at)
                  - app_config         (persisted user settings, JSONB)

                          ^
                          | enrichGeoIP() injects coords
                          |
                  internal/geoip/geoip.go (Locator)
                  - mmdb / ipapi / ipinfo / ipwhois
                  - In-memory TTL cache
```

The store is the only thing that talks to either database. All HTTP handlers and the scheduled task call methods on `*store.Store`.

## Module Responsibilities

| Path | Role |
| --- | --- |
| `internal/runtime/runtime.go` | Implements `Runtime.Configure`. Parses global config into `Config`, applies `NormalizeAppConfig` (floors and defaults), calls the `onConfig` callback in `main.go`. Holds the last-applied config under a mutex for inspection — though nothing else currently reads it. |
| `internal/store/store.go` | All SQL. Two pools (`pool` plugin-owned, `sourcePool` Continuum). Owns `Migrate`, sessions/counts/map/history queries, cursor management, retention pruning, `app_config` persistence. The `GeoLocator` interface is consumed here for `MapSessions` enrichment. |
| `internal/server/server.go` | `chi` HTTP router. `requireAdmin` middleware checks `X-Continuum-User-Role`. `hOverview` is the SPA's main poll and the only handler that builds per-section health. SPA shell is served via `hSPA` with asset path rewriting and theme injection. |
| `internal/httproutes/server.go` | Thin adapter exposing `SetHandler` so `main.go` can swap routers on reconfigure without restarting the gRPC server. |
| `internal/poll/scheduled.go` | Implements `ScheduledTask.Run`. Holds `atomic.Pointer` to `*store.Store` and `*RetentionPolicy`; both are nil before first successful `Configure`. Calls `SyncPlaybackHistory` (the full-batch path, up to 20 batches of 5000 rows). |
| `internal/geoip/geoip.go` | Provider-aware locator with TTL cache. See [`geoip.md`](geoip.md). |
| `cmd/.../main.go` | Wiring. Loads embedded manifest, computes binary checksum, builds capability servers, runs the SDK serve loop. |
| `web/` | React SPA. Built with Vite, embedded via `go:embed web/dist`. |

## Realtime vs Scheduled History Sync

There are two entry points to `syncPlaybackHistory` with different concurrency rules:

- **Scheduled** (`SyncPlaybackHistory`, called by the cron via `poll.Run`): unlocks the mutex with `Lock()` — blocks if a realtime sync is in flight. Pages up to 20 batches of 5000 rows.
- **Realtime** (`SyncPlaybackHistoryRealtime`, called via `PlaybackHistory` realtime path): uses `TryLock` and returns silently if locked. Also rate-limited to once per 10 seconds via `lastRealtimeHistorySync`. Pages 1 batch only.

Note: the SPA currently uses `PlaybackHistoryReadOnly` (no sync trigger). Realtime sync is wired but only exercised if a caller passes `realtime=true` to `PlaybackHistory`. Pruning still runs at the end of every sync invocation.

The cursor is `(ended_at, session_id)` — the lexicographic ordering avoids missing rows that share an `ended_at` timestamp. The 2-minute rewind handles the case where new rows land in the source DB with an `ended_at` slightly earlier than the cursor (clock skew, slow inserts).

## Health Banner Wiring

`hOverview` runs each of the four queries independently and records per-section errors in the `health` map without aborting:

```go
counts, err := d.Store.Counts(...)
if err != nil { resp.Health["counts"] = sectionHealth{OK: false, Code: "counts_failed", ...} }
```

The SPA reads `health.{counts,sessions,map,history}` to render the banner cells. Adding a new panel? Add another `sectionHealth` entry and a matching query — the contract is just "any error -> one bad cell, never a global failure".

## SPA Asset Rewriting

The plugin is mounted under a host-controlled prefix (Continuum routes it as `/api/v1/plugins/<install-id>/...`). The SPA's embedded `index.html` has `/assets/...` absolute references baked in by Vite. `rewritePluginAssets` rewrites those to relative paths based on the current request:

- Top-level (`/admin`, `/dashboard`, `/`) — `assets/foo.js`
- Deep links (`/admin/anything`) — `../assets/foo.js`

The `pluginBaseHref` helper is unused in the current build but exists for the case where the host wants an absolute base injected. If the SPA breaks after a route change, this is the function to inspect.

## App Config Persistence

`app_config` is a singleton JSONB row. Reads use `GetAppConfig` -> unmarshal into `pluginrt.Config` -> `NormalizeAppConfig`. Writes use `UpdateAppConfig` -> `NormalizeAppConfig` -> marshal -> upsert. The DSNs are explicitly zeroed before persisting (so they can't leak via `GET /api/config`).

`ImportLegacyAppConfig` is called once per `Configure` and only writes if the stored config equals `DefaultAppConfig()` — i.e., it migrates "no persisted config" to "global config snapshot" but otherwise refuses to overwrite operator changes. This is the source of the "host config didn't apply" gotcha covered in `setup-debug-flows.md`.

## What Is Not Here

- No event consumption (`event.v1` capability not registered). Plugin is purely poll-driven from the source DB.
- No outbound webhooks or notifications. For cross-plugin events, the `continuum-plugin-notifications` hub is the intended path; this plugin doesn't publish to it today.
- No background goroutines beyond what `pgxpool` and the host's scheduled-task runner provide. All work happens on request or on the cron tick.
- No SSE/websocket. The 30 s poll is the only refresh mechanism.
