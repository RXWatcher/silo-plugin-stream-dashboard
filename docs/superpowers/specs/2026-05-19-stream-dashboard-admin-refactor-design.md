# Stream Dashboard Admin Refactor Design

Date: 2026-05-19
Plugin: `continuum.stream-dashboard`
Status: Proposed

## Summary

`continuum.stream-dashboard` currently mixes user-facing routes, admin controls, live operational views, history ingestion, and plugin configuration into one SPA. The result is a dashboard that looks active while hiding backend failures, exposes mutable config from a broadly authenticated route, and couples page reads to ingestion and retention work.

This refactor turns the plugin into an admin-only operations console. It keeps the existing plugin/runtime model and data sources, but tightens access control, separates prepared-state reads from sync work, and restructures the UI around focused admin tasks instead of a single mixed-purpose page.

## Problems To Fix

1. Any authenticated user can currently hit `/api/*`, including config endpoints.
2. The SPA exposes a settings editor from the main dashboard shell.
3. `/api/overview` returns `200 OK` with zeroed data when counts, sessions, map, or history fail, which renders as a healthy empty state instead of an operational failure.
4. Dashboard reads trigger realtime history sync and retention work.
5. `/dashboard` and `/admin` render the same shell with nearly the same behavior.
6. The frontend is a single-file app with presentation, fetching, and state management collapsed together.
7. The map/globe dominates the page even though it is a secondary diagnostic view.

## Goals

1. Make the entire plugin surface admin-only.
2. Surface dependency and sync failures explicitly in both API responses and UI.
3. Ensure dashboard reads use prepared plugin state instead of triggering ingestion and pruning.
4. Split the frontend into focused admin sections with clearer hierarchy.
5. Preserve the existing plugin stack: Go backend, embedded SPA frontend, scheduled sync task.
6. Keep the refactor reviewable and incremental rather than rebuilding the plugin from scratch.

## Non-Goals

1. Rebuild the plugin into a multi-page Next.js app like `server-manager`.
2. Add websockets, Redis, or new infrastructure dependencies.
3. Add session termination, sharing detection, or analytics rule engines.
4. Replace the map/globe feature entirely.
5. Change the plugin manifest capability model beyond access control and route intent.

## Chosen Approach

Use the current plugin architecture, but change the contract from "authenticated dashboard with embedded admin knobs" to "admin-only operations console."

This is the smallest change that fixes the serious flaws:

1. Access control becomes explicit at the manifest and handler levels.
2. Data endpoints expose section health instead of fake zero states.
3. History sync becomes a background concern with an optional explicit admin action, not an implicit page-read side effect.
4. The frontend becomes modular and task-oriented while staying inside the existing Vite/React SPA.

## Alternatives Considered

### Option A: Minimal patch

Gate `/api/config`, remove the settings button from user routes, and show a banner when overview payloads include errors.

Pros:
- Fastest path
- Low code churn

Cons:
- Leaves `/dashboard` and `/admin` conceptually muddled
- Leaves read-path sync coupling in place
- Leaves single-file SPA structure mostly intact

### Option B: Recommended

Make the entire plugin admin-only, decouple sync from reads, and restructure the SPA into focused admin panels.

Pros:
- Fixes the actual security and operations problems
- Keeps the current plugin architecture
- Delivers a clearer UI without a full rewrite

Cons:
- Moderate backend and frontend churn
- Requires contract changes to tests

### Option C: Full product-area rebuild

Recreate the `server-manager` model with multi-page routing, richer analytics, and live push updates.

Pros:
- Highest long-term ceiling

Cons:
- Overkill for this plugin
- Large surface area and regression risk
- Requires infrastructure the plugin does not currently need

## Backend Design

### Access Model

The plugin becomes admin-only end to end.

Changes:

1. Change `/dashboard` and `/dashboard/*` to `admin` access in the manifest.
2. Keep `/admin` and `/admin/*` as admin access.
3. Restrict `/api/config` reads and writes to admin-only behavior.
4. Treat the plugin as an admin operations console, not a user application.

Expected result:

- Non-admins should not be able to open the dashboard route.
- Non-admins should not be able to read or mutate plugin config.

### Overview Contract

`/api/overview` should no longer silently coerce operational failures into empty data.

New behavior:

1. Return a structured overview payload with section-level health.
2. Include explicit status for `counts`, `sessions`, `map`, and `history`.
3. If a section fails, keep healthy sections when possible and surface failure metadata for the broken section.
4. Reserve transport-level failures for total request failure, not partial section issues.

Proposed response shape:

```json
{
  "generated_at": "2026-05-19T00:00:00Z",
  "refresh_seconds": 30,
  "counts": { "...": "..." },
  "sessions": [],
  "map_sessions": [],
  "history": { "...": "..." },
  "health": {
    "counts": { "ok": true },
    "sessions": { "ok": true },
    "map": { "ok": false, "code": "map_failed", "message": "..." },
    "history": { "ok": true }
  }
}
```

The frontend will treat `health.*.ok === false` as an operational warning state, not as empty business data.

### History Read Path

History reads should stop performing sync work automatically.

Changes:

1. `PlaybackHistory(...)` should gain a pure read mode that only queries plugin-owned tables.
2. `hOverview` and `hHistory` should use read-only history retrieval.
3. The scheduled task remains the primary sync mechanism.
4. Add an explicit admin-triggered sync endpoint or action only if needed for operator recovery.

If a manual sync action is added, it should:

1. Be admin-only.
2. Be clearly presented as a maintenance action.
3. Return sync result metadata such as synced row count, pruned row count, and last sync time.

### Status And Health

The backend should expose enough health information for the UI to be honest.

Add or expand:

1. Last history sync timestamp.
2. Whether history has ever synced successfully.
3. Whether source DB queries are currently succeeding.
4. Whether map enrichment is disabled or unavailable.

This health data should be cheap to query and safe to render on every refresh.

## Frontend Design

### Information Architecture

The SPA should become a compact admin console with four main sections:

1. Overview
2. Active sessions
3. Playback history
4. Settings and health

Map/globe becomes a secondary diagnostic tab inside the overview area instead of the dominant hero surface.

### Layout

The page should prioritize operational decisions:

1. Top header with title, live status, refresh action, and last update.
2. Primary overview metrics row.
3. Health banner area for partial failures and stale sync state.
4. Main content split:
   - active sessions and playback history as primary panels
   - map/globe and server/media breakdown as secondary panels
5. Settings in a distinct admin panel, not embedded as an inline notice block.

### Behavior

1. Remove automatic config fetch/edit exposure from a generic dashboard shell.
2. Load settings only when the settings panel is opened.
3. Render explicit warning cards for failed sections such as history sync, source DB read failure, or map lookup failure.
4. Preserve manual refresh, but make it a data refresh only.
5. Keep empty states for real empty data, but distinguish them from degraded health states.

### Component Structure

Break `web/src/main.tsx` into focused components/modules:

1. `AppShell`
2. `OverviewMetrics`
3. `HealthBanner`
4. `MapPanel`
5. `SessionsPanel`
6. `HistoryPanel`
7. `SettingsPanel`
8. `api.ts` or equivalent fetch helpers
9. Shared formatting helpers

The goal is to remove the current single-file control plane without introducing unnecessary abstraction.

## API And UI Error Handling

### Partial Data

If `counts` succeeds and `history` fails:

1. Show metrics from `counts`.
2. Show a warning banner that history is degraded.
3. Show the history panel in an error state, not "No playback history has been synced yet."

### Total Failure

If the entire overview request fails:

1. Show a top-level failure state.
2. Keep the last successfully rendered data only if the UI clearly marks it as stale.
3. Do not replace prior data with fake zeros.

### Config Save

Config save remains explicit:

1. Admin-only.
2. Inline success/error feedback.
3. Clear message when runtime restart or reconfigure is required.

## Testing Strategy

### Backend Tests

Add tests for:

1. Admin-only route expectations in manifest-sensitive handlers.
2. Config endpoint access restrictions.
3. Overview partial-failure payload behavior.
4. History read path not invoking sync logic.

### Frontend Tests

Add tests for:

1. Health banner rendering from degraded overview payloads.
2. Distinguishing empty states from error states.
3. Settings panel access and lazy load behavior.
4. Removal of user-facing config controls from the default shell.

### Verification

Run:

1. `go test ./...`
2. `pnpm test`
3. `pnpm build`

If the refactor introduces new helpers or components, verify that route mounting and asset rewriting still work for `/admin` and `/dashboard`.

## Implementation Plan Shape

The implementation should happen in this order:

1. Tighten manifest and backend access control.
2. Introduce explicit overview health contract.
3. Decouple history read path from sync behavior.
4. Refactor frontend data layer and split components.
5. Rework layout and settings placement.
6. Add or update tests.
7. Run full verification.

## Risks

1. Tightening route access may require updating E2E expectations outside this repo.
2. Changing `/api/overview` shape may break any undocumented consumers.
3. Removing read-triggered sync means dashboards depend more heavily on the scheduled task being configured correctly.

## Mitigations

1. Keep the main data fields stable and additive where possible.
2. Preserve existing route paths even as access changes.
3. Surface last sync time and health clearly so operators can diagnose missing background sync.

## Success Criteria

The refactor is successful when:

1. Non-admin users cannot access any stream dashboard routes or config endpoints.
2. The UI never presents dependency failures as legitimate zero states.
3. Loading the dashboard does not trigger history sync or retention pruning.
4. The frontend is split into focused modules instead of one large entry file.
5. The dashboard reads as an admin operations console rather than a mixed user/admin toy view.
