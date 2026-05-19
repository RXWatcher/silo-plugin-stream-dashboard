# Stream Dashboard Admin Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `continuum.stream-dashboard` into an admin-only operations console that surfaces degraded backend state honestly and no longer performs sync work during dashboard reads.

**Architecture:** Keep the existing Go backend, scheduled sync task, and embedded React SPA. Tighten route access in the manifest and handlers, change overview responses to include section health instead of fake zero states, split history sync from history reads, and refactor the SPA into focused admin panels that render health and settings explicitly.

**Tech Stack:** Go, Chi, pgx, React 19, Vite, Vitest, TypeScript, CSS

---

## File Structure

- Modify: `cmd/continuum-plugin-stream-dashboard/manifest.json`
  Route access contract for `/dashboard`, `/admin`, and `/api/*`.
- Modify: `internal/server/server.go`
  HTTP route wiring, access checks for config/manual-admin endpoints, overview response contract.
- Modify: `internal/server/server_test.go`
  Regression tests for access checks and overview degraded responses.
- Modify: `internal/store/store.go`
  Separate read-only playback history queries from sync-triggering queries.
- Modify: `internal/store/store_test.go`
  Tests for read-only history behavior.
- Create: `web/src/api.ts`
  Frontend fetch helpers and overview/config response typing.
- Create: `web/src/components/AppShell.tsx`
  Top-level admin console composition.
- Create: `web/src/components/HealthBanner.tsx`
  Degraded-state and stale-state rendering.
- Create: `web/src/components/OverviewMetrics.tsx`
  Summary cards and status row.
- Create: `web/src/components/MapPanel.tsx`
  Map/globe panel with secondary diagnostic treatment.
- Create: `web/src/components/SessionsPanel.tsx`
  Active session rendering.
- Create: `web/src/components/HistoryPanel.tsx`
  Playback history rendering and degraded history state.
- Create: `web/src/components/SettingsPanel.tsx`
  Admin-only lazy settings load/save.
- Modify: `web/src/main.tsx`
  Reduce to bootstrapping plus high-level state orchestration.
- Modify: `web/src/types.ts`
  Add overview health typing.
- Modify: `web/src/styles.css`
  Updated admin-console layout and health/settings panels.
- Create: `web/src/components/AppShell.test.tsx`
  UI regression tests for degraded health and settings placement.

---

### Task 1: Tighten manifest and server access rules

**Files:**
- Modify: `cmd/continuum-plugin-stream-dashboard/manifest.json`
- Modify: `internal/server/server.go`
- Test: `internal/server/server_test.go`

- [ ] **Step 1: Write the failing backend tests for admin-only access expectations**

```go
func TestDashboardRoutesRequireAdminAccessInManifest(t *testing.T) {
	body, err := os.ReadFile("../../cmd/continuum-plugin-stream-dashboard/manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		HTTPRoutes []struct {
			Path   string `json:"path"`
			Access string `json:"access"`
		} `json:"http_routes"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}

	accessByPath := map[string]string{}
	for _, route := range manifest.HTTPRoutes {
		accessByPath[route.Path] = route.Access
	}

	if got := accessByPath["/dashboard"]; got != "admin" {
		t.Fatalf("dashboard access = %q, want admin", got)
	}
	if got := accessByPath["/dashboard/*"]; got != "admin" {
		t.Fatalf("dashboard app access = %q, want admin", got)
	}
}

func TestConfigEndpointRejectsNonAdminRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(`{}`))
	req.Header.Set("X-Continuum-Role", "user")
	rec := httptest.NewRecorder()

	h := New(Deps{Store: &store.Store{}})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
```

- [ ] **Step 2: Run the targeted backend tests to verify they fail**

Run: `go test ./internal/server -run 'TestDashboardRoutesRequireAdminAccessInManifest|TestConfigEndpointRejectsNonAdminRequests'`

Expected: FAIL because the manifest still exposes `/dashboard` as authenticated and the server does not yet gate `/api/config`.

- [ ] **Step 3: Implement the minimal manifest and handler access changes**

```json
{
  "id": "api",
  "method": "*",
  "path": "/api/*",
  "access": "admin"
},
{
  "id": "dashboard_root",
  "method": "GET",
  "path": "/dashboard",
  "access": "admin",
  "navigable": true,
  "navigation_label": "Stream Dashboard",
  "navigation_kind": "admin"
}
```

```go
func requireAdmin(r *http.Request) error {
	role := strings.TrimSpace(r.Header.Get("X-Continuum-Role"))
	if strings.EqualFold(role, "admin") {
		return nil
	}
	return errForbidden
}

func hGetConfig(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireAdmin(r); err != nil {
			writeErr(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		cfg, err := d.Store.GetAppConfig(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "config_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}
```

- [ ] **Step 4: Run the targeted backend tests to verify they pass**

Run: `go test ./internal/server -run 'TestDashboardRoutesRequireAdminAccessInManifest|TestConfigEndpointRejectsNonAdminRequests'`

Expected: PASS

- [ ] **Step 5: Commit the access-control slice**

```bash
git add cmd/continuum-plugin-stream-dashboard/manifest.json \
  internal/server/server.go \
  internal/server/server_test.go
git commit -m "feat: make stream dashboard admin only"
```

---

### Task 2: Change overview responses to expose degraded section health

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`
- Modify: `web/src/types.ts`

- [ ] **Step 1: Write the failing overview health contract test**

```go
func TestOverviewReturnsSectionHealthWhenHistoryFails(t *testing.T) {
	st := newStubStore()
	st.counts = store.Counts{
		Servers: store.ServerSummary{Total: 1, Online: 1, Offline: 0, ByType: map[string]int{"continuum": 1}},
		Sessions: store.SessionCounts{Active: 1, DirectPlay: 1},
	}
	st.historyErr = errors.New("history query failed")

	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()

	New(Deps{Store: st, RefreshSeconds: 30}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var payload struct {
		Health map[string]struct {
			OK      bool   `json:"ok"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"health"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	if payload.Health["history"].OK {
		t.Fatal("history health unexpectedly ok")
	}
	if payload.Health["history"].Code != "history_failed" {
		t.Fatalf("history code = %q", payload.Health["history"].Code)
	}
}
```

- [ ] **Step 2: Run the targeted backend test to verify it fails**

Run: `go test ./internal/server -run TestOverviewReturnsSectionHealthWhenHistoryFails`

Expected: FAIL because overview currently returns the zeroed `emptyOverview` payload instead of section health.

- [ ] **Step 3: Implement section-health overview assembly**

```go
type sectionHealth struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type overviewResponse struct {
	Counts         store.Counts                  `json:"counts"`
	Sessions       []store.Session               `json:"sessions"`
	MapSessions    []store.MapSession            `json:"map_sessions"`
	History        store.PlaybackHistoryPage     `json:"history"`
	RefreshSeconds int                           `json:"refresh_seconds"`
	GeneratedAt    time.Time                     `json:"generated_at"`
	Health         map[string]sectionHealth      `json:"health"`
}
```

```go
func hOverview(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := overviewResponse{
			Counts:         zeroCounts(),
			Sessions:       []store.Session{},
			MapSessions:    []store.MapSession{},
			History:        zeroHistoryPage(20),
			RefreshSeconds: refreshSeconds(d.RefreshSeconds),
			GeneratedAt:    time.Now().UTC(),
			Health: map[string]sectionHealth{
				"counts":   {OK: true},
				"sessions": {OK: true},
				"map":      {OK: true},
				"history":  {OK: true},
			},
		}

		if counts, err := d.Store.Counts(r.Context()); err != nil {
			resp.Health["counts"] = sectionHealth{OK: false, Code: "counts_failed", Message: err.Error()}
		} else {
			resp.Counts = counts
		}

		if sessions, err := d.Store.Sessions(r.Context()); err != nil {
			resp.Health["sessions"] = sectionHealth{OK: false, Code: "sessions_failed", Message: err.Error()}
		} else {
			resp.Sessions = sessions
		}

		if mapSessions, err := d.Store.MapSessions(r.Context()); err != nil {
			resp.Health["map"] = sectionHealth{OK: false, Code: "map_failed", Message: err.Error()}
		} else {
			resp.MapSessions = mapSessions
		}

		if history, err := d.Store.PlaybackHistoryReadOnly(r.Context(), 20, 0); err != nil {
			resp.Health["history"] = sectionHealth{OK: false, Code: "history_failed", Message: err.Error()}
		} else {
			resp.History = history
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
```

- [ ] **Step 4: Add frontend types for health-aware overview payloads**

```ts
export type SectionHealth = {
  ok: boolean;
  code?: string;
  message?: string;
};

export type OverviewHealth = {
  counts: SectionHealth;
  sessions: SectionHealth;
  map: SectionHealth;
  history: SectionHealth;
};

export type Overview = {
  counts: Counts;
  sessions: Session[];
  map_sessions: MapSession[];
  history: PlaybackHistoryPage;
  refresh_seconds: number;
  generated_at: string;
  health: OverviewHealth;
};
```

- [ ] **Step 5: Run the targeted backend test to verify it passes**

Run: `go test ./internal/server -run TestOverviewReturnsSectionHealthWhenHistoryFails`

Expected: PASS

- [ ] **Step 6: Commit the overview contract slice**

```bash
git add internal/server/server.go internal/server/server_test.go web/src/types.ts
git commit -m "feat: expose dashboard section health"
```

---

### Task 3: Decouple playback history reads from sync and retention work

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Write the failing store test for pure read behavior**

```go
func TestPlaybackHistoryReadOnlyDoesNotAdvanceSyncState(t *testing.T) {
	st, ctx := newTestStore(t)

	before, _, err := st.historyCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.PlaybackHistoryReadOnly(ctx, 20, 0); err != nil {
		t.Fatal(err)
	}

	after, _, err := st.historyCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !after.Equal(before) {
		t.Fatalf("history cursor advanced during read-only query")
	}
}
```

- [ ] **Step 2: Run the targeted store test to verify it fails**

Run: `go test ./internal/store -run TestPlaybackHistoryReadOnlyDoesNotAdvanceSyncState`

Expected: FAIL because read-only history retrieval does not exist yet.

- [ ] **Step 3: Extract the history query into a pure read helper**

```go
func (s *Store) PlaybackHistoryReadOnly(ctx context.Context, limit, offset int) (PlaybackHistoryPage, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	lastSync, err := s.LastHistorySyncAt(ctx)
	if err != nil {
		return PlaybackHistoryPage{}, err
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM playback_history`).Scan(&total); err != nil {
		return PlaybackHistoryPage{}, err
	}

	items, err := s.playbackHistoryItems(ctx, limit, offset)
	if err != nil {
		return PlaybackHistoryPage{}, err
	}

	return PlaybackHistoryPage{
		Items:      items,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		SyncedRows: 0,
		PrunedRows: 0,
		LastSyncAt: lastSync,
	}, nil
}
```

```go
func (s *Store) PlaybackHistory(ctx context.Context, limit, offset int, policy RetentionPolicy, realtime bool) (PlaybackHistoryPage, error) {
	synced, pruned, err := s.syncHistoryForRead(ctx, policy, realtime)
	if err != nil {
		return PlaybackHistoryPage{}, err
	}

	page, err := s.PlaybackHistoryReadOnly(ctx, limit, offset)
	if err != nil {
		return PlaybackHistoryPage{}, err
	}
	page.SyncedRows = synced
	page.PrunedRows = pruned
	return page, nil
}
```

- [ ] **Step 4: Update server history/overview routes to use pure reads**

```go
func hHistory(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := boundedIntQuery(r, "page", 1, 1, 10000)
		limit := boundedIntQuery(r, "limit", 50, 1, 500)
		history, err := d.Store.PlaybackHistoryReadOnly(r.Context(), limit, (page-1)*limit)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "history_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, history)
	}
}
```

- [ ] **Step 5: Run the targeted store and server tests to verify they pass**

Run: `go test ./internal/store -run TestPlaybackHistoryReadOnlyDoesNotAdvanceSyncState && go test ./internal/server -run TestOverviewReturnsSectionHealthWhenHistoryFails`

Expected: PASS

- [ ] **Step 6: Commit the history decoupling slice**

```bash
git add internal/store/store.go internal/store/store_test.go internal/server/server.go
git commit -m "refactor: decouple history reads from sync"
```

---

### Task 4: Split the SPA into admin panels and render degraded state explicitly

**Files:**
- Create: `web/src/api.ts`
- Create: `web/src/components/AppShell.tsx`
- Create: `web/src/components/HealthBanner.tsx`
- Create: `web/src/components/OverviewMetrics.tsx`
- Create: `web/src/components/MapPanel.tsx`
- Create: `web/src/components/SessionsPanel.tsx`
- Create: `web/src/components/HistoryPanel.tsx`
- Create: `web/src/components/SettingsPanel.tsx`
- Create: `web/src/components/AppShell.test.tsx`
- Modify: `web/src/main.tsx`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Write the failing frontend test for degraded history rendering**

```tsx
it("renders a degraded history warning instead of an empty state", async () => {
  render(
    <AppShell
      overview={{
        counts: emptyCounts,
        sessions: [],
        map_sessions: [],
        history: { items: [], total: 0, limit: 20, offset: 0, synced_rows: 0, pruned_rows: 0 },
        refresh_seconds: 30,
        generated_at: new Date().toISOString(),
        health: {
          counts: { ok: true },
          sessions: { ok: true },
          map: { ok: true },
          history: { ok: false, code: "history_failed", message: "history query failed" },
        },
      }}
      loading={false}
      error=""
      onRefresh={vi.fn()}
    />
  );

  expect(screen.getByText(/history query failed/i)).toBeInTheDocument();
  expect(screen.queryByText(/No playback history has been synced yet/i)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run the targeted frontend test to verify it fails**

Run: `pnpm test -- AppShell.test.tsx`

Expected: FAIL because the app is still a single-file shell and has no degraded history component.

- [ ] **Step 3: Add the frontend data helpers and panel components**

```ts
// web/src/api.ts
export async function fetchOverview(signal?: AbortSignal): Promise<Overview> {
  const response = await fetch(`${pluginMountPath()}/api/overview`, {
    signal,
    headers: { Accept: "application/json", ...authHeaders() },
  });
  if (!response.ok) {
    throw new Error(await response.text() || `Request failed with ${response.status}`);
  }
  return normalizeOverview(await response.json() as Partial<Overview>);
}
```

```tsx
// web/src/components/HealthBanner.tsx
export function HealthBanner({ health }: { health: OverviewHealth }) {
  const degraded = Object.entries(health).filter(([, section]) => !section.ok);
  if (!degraded.length) return null;

  return (
    <section className="health-banner" role="alert">
      {degraded.map(([key, section]) => (
        <article key={key} className="health-banner__item">
          <strong>{key}</strong>
          <span>{section.message || "Section degraded"}</span>
        </article>
      ))}
    </section>
  );
}
```

```tsx
// web/src/components/SettingsPanel.tsx
export function SettingsPanel({ open, onToggle }: SettingsPanelProps) {
  const [settingsJSON, setSettingsJSON] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    if (!open) return;
    fetchConfig()
      .then((cfg) => setSettingsJSON(JSON.stringify(cfg, null, 2)))
      .catch((err) => setStatus((err as Error).message));
  }, [open]);

  if (!open) {
    return <button className="panel-toggle" onClick={onToggle} type="button">Settings</button>;
  }

  return (
    <section className="settings-panel">
      <textarea value={settingsJSON} onChange={(event) => setSettingsJSON(event.target.value)} spellCheck={false} />
      {status ? <p>{status}</p> : null}
    </section>
  );
}
```

- [ ] **Step 4: Reduce `main.tsx` to orchestration and mount the new shell**

```tsx
function App() {
  const [overview, setOverview] = useState<Overview>(fallbackOverview);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    void refreshOverview(controller.signal, setOverview, setError, setLoading);
    return () => controller.abort();
  }, []);

  return (
    <AppShell
      overview={overview}
      loading={loading}
      error={error}
      onRefresh={() => void refreshOverview(undefined, setOverview, setError, setLoading)}
    />
  );
}
```

- [ ] **Step 5: Update styles for admin-console hierarchy**

```css
.health-banner {
  display: grid;
  gap: 12px;
  margin: 0 auto 18px;
  max-width: 1480px;
}

.health-banner__item {
  padding: 14px 16px;
  border: 1px solid rgba(249, 199, 79, 0.35);
  border-radius: 12px;
  background: rgba(96, 62, 16, 0.24);
}

.settings-panel {
  display: grid;
  gap: 12px;
  padding: 18px;
  border: 1px solid var(--dashboard-border);
  border-radius: 12px;
  background: var(--dashboard-panel);
}
```

- [ ] **Step 6: Run the targeted frontend test to verify it passes**

Run: `pnpm test -- AppShell.test.tsx`

Expected: PASS

- [ ] **Step 7: Commit the SPA refactor slice**

```bash
git add web/src/main.tsx web/src/types.ts web/src/styles.css web/src/api.ts web/src/components
git commit -m "refactor: split dashboard into admin panels"
```

---

### Task 5: Full verification and cleanup

**Files:**
- Modify: any touched files from previous tasks only if verification reveals regressions

- [ ] **Step 1: Run backend tests**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 2: Run frontend tests**

Run: `pnpm test`

Expected: PASS

- [ ] **Step 3: Run frontend build**

Run: `pnpm build`

Expected: PASS with Vite production build output

- [ ] **Step 4: Check repository diff**

Run: `git status --short`

Expected: Only intended source changes remain

- [ ] **Step 5: Commit final verification fixes if needed**

```bash
git add cmd/continuum-plugin-stream-dashboard/manifest.json \
  internal/server/server.go \
  internal/server/server_test.go \
  internal/store/store.go \
  internal/store/store_test.go \
  web/src
git commit -m "test: finalize stream dashboard admin refactor"
```

---

## Self-Review

Spec coverage:

1. Admin-only access is covered by Task 1.
2. Overview health and honest degraded state are covered by Task 2 and Task 4.
3. Read-path sync removal is covered by Task 3.
4. Frontend modularization and settings placement are covered by Task 4.
5. Verification is covered by Task 5.

Placeholder scan:

1. No `TODO`, `TBD`, or "appropriate handling" placeholders remain.
2. Every task includes exact file targets and exact commands.

Type consistency:

1. `Overview.health` is defined in Task 2 and consumed in Task 4.
2. `PlaybackHistoryReadOnly` is introduced in Task 3 and used by the server changes in Task 2 and Task 3.
