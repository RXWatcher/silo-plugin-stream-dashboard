import React, { Suspense, lazy, useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  AlertCircle,
  ArrowLeft,
  Circle,
  Clock3,
  Cpu,
  Database,
  Film,
  Globe2,
  Map,
  MonitorPlay,
  RefreshCw,
  Server,
  Timer,
  Users,
} from "lucide-react";
import { hasCoords } from "./geo";
import { pluginMountPath } from "./mountPath";
import { adminBackTarget } from "./navigation";
import WorldMap from "./WorldMap";
import type { Counts, Overview, PlaybackHistoryItem, PlaybackHistoryPage, Session } from "./types";
import "./styles.css";

type ViewMode = "map" | "globe";

const GlobeView = lazy(() => import("./GlobeView"));

let cachedToken = "";

function captureTokenFromURL(): void {
  const params = new URLSearchParams(window.location.search);
  cachedToken = params.get("token") || "";
  const theme = params.get("theme") || sessionStorage.getItem("continuum-theme") || "";
  if (theme) {
    document.documentElement.dataset.theme = theme;
    try {
      sessionStorage.setItem("continuum-theme", theme);
    } catch {
      // Ignore storage failures in private browsing contexts.
    }
  }
  if (!params.has("token")) return;
  params.delete("token");
  const clean = window.location.pathname + (params.toString() ? `?${params.toString()}` : "") + window.location.hash;
  window.history.replaceState(null, "", clean);
}

captureTokenFromURL();

function authHeaders(): Record<string, string> {
  return cachedToken ? { Authorization: `Bearer ${cachedToken}` } : {};
}

const emptyCounts: Counts = {
  servers: { total: 0, online: 0, offline: 0, by_type: {} },
  sessions: { active: 0, transcoding: 0, direct_play: 0 },
  history: { total: 0, today: 0, this_week: 0, this_month: 0 },
  users: { unique: 0, active_today: 0, active_this_week: 0 },
  media: {},
};

const fallbackOverview: Overview = {
  counts: emptyCounts,
  sessions: [],
  map_sessions: [],
  history: { items: [], total: 0, limit: 20, offset: 0, synced_rows: 0, pruned_rows: 0 },
  refresh_seconds: 30,
  generated_at: new Date(0).toISOString(),
};

async function fetchOverview(signal?: AbortSignal): Promise<Overview> {
  const base = pluginMountPath();
  const response = await fetch(`${base}/api/overview`, {
    signal,
    headers: { Accept: "application/json", ...authHeaders() },
  });
  if (!response.ok) {
    const body = await response.text();
    throw new Error(body || `Request failed with ${response.status}`);
  }
  const payload = (await response.json()) as Partial<Overview>;
  return normalizeOverview(payload);
}

async function fetchConfig(): Promise<Record<string, unknown>> {
  const response = await fetch(`${pluginMountPath()}/api/config`, {
    headers: { Accept: "application/json", ...authHeaders() },
  });
  if (!response.ok) throw new Error(await response.text());
  return (await response.json()) as Record<string, unknown>;
}

async function saveConfigJSON(raw: string): Promise<Record<string, unknown>> {
  const parsed = JSON.parse(raw) as Record<string, unknown>;
  const response = await fetch(`${pluginMountPath()}/api/config`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", ...authHeaders() },
    body: JSON.stringify(parsed),
  });
  if (!response.ok) throw new Error(await response.text());
  return (await response.json()) as Record<string, unknown>;
}

function normalizeOverview(payload: Partial<Overview>): Overview {
  const history = payload.history ?? fallbackOverview.history;
  return {
    ...fallbackOverview,
    ...payload,
    counts: payload.counts ?? fallbackOverview.counts,
    sessions: Array.isArray(payload.sessions) ? payload.sessions : [],
    map_sessions: Array.isArray(payload.map_sessions) ? payload.map_sessions : [],
    history: {
      ...fallbackOverview.history,
      ...history,
      items: Array.isArray(history.items) ? history.items : [],
    },
  };
}

function App() {
  const [overview, setOverview] = useState<Overview>(fallbackOverview);
  const [error, setError] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [viewMode, setViewMode] = useState<ViewMode>("globe");
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsJSON, setSettingsJSON] = useState("");
  const [settingsStatus, setSettingsStatus] = useState("");

  const refresh = async (signal?: AbortSignal) => {
    setLoading(true);
    try {
      const next = await fetchOverview(signal);
      setOverview(next);
      setLastUpdated(new Date());
      setError("");
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        setError((err as Error).message);
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    const controller = new AbortController();
    refresh(controller.signal);
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const seconds = Math.max(overview.refresh_seconds || 30, 5);
    const id = window.setInterval(() => refresh(), seconds * 1000);
    return () => window.clearInterval(id);
  }, [overview.refresh_seconds]);

  useEffect(() => {
    fetchConfig()
      .then((cfg) => setSettingsJSON(JSON.stringify(cfg, null, 2)))
      .catch(() => setSettingsJSON(""));
  }, []);

  async function saveSettings() {
    setSettingsStatus("");
    try {
      const cfg = await saveConfigJSON(settingsJSON);
      setSettingsJSON(JSON.stringify(cfg, null, 2));
      setSettingsStatus("Settings saved. Restart or reconfigure the plugin to apply runtime changes.");
    } catch (err) {
      setSettingsStatus((err as Error).message);
    }
  }

  const mapSessions = Array.isArray(overview.map_sessions) ? overview.map_sessions : [];
  const points = useMemo(() => mapSessions.filter((s) => hasCoords(s.client)), [mapSessions]);
  const activeSessions = Array.isArray(overview.sessions) ? overview.sessions : [];
  const counts = overview.counts;
  const history = overview.history?.items ? overview.history : fallbackOverview.history;
  const backTarget = adminBackTarget(window.location.pathname);

  return (
    <main className="app-shell">
      <header className="topbar">
        <div>
          <a className="back-link" href={backTarget.href} title={backTarget.title}>
            <ArrowLeft size={16} />
            <span>{backTarget.label}</span>
          </a>
          <p className="eyebrow">Continuum plugin</p>
          <h1>Stream Dashboard</h1>
        </div>
        <div className="topbar-actions">
          <div className="segmented" aria-label="Map display mode">
            <button className={viewMode === "map" ? "active" : ""} onClick={() => setViewMode("map")} type="button" title="Map view">
              <Map size={18} />
              <span>Map</span>
            </button>
            <button className={viewMode === "globe" ? "active" : ""} onClick={() => setViewMode("globe")} type="button" title="Globe view">
              <Globe2 size={18} />
              <span>Globe</span>
            </button>
          </div>
          <button className="icon-button" onClick={() => refresh()} type="button" title="Refresh">
            <RefreshCw size={18} className={loading ? "spin" : ""} />
          </button>
          <button className="icon-button" onClick={() => setSettingsOpen((v) => !v)} type="button" title="Settings">
            <Database size={18} />
          </button>
        </div>
      </header>

      {error ? (
        <section className="notice" role="alert">
          <AlertCircle size={18} />
          <span>{friendlyError(error)}</span>
        </section>
      ) : null}

      {settingsOpen ? (
        <section className="notice">
          <Database size={18} />
          <div className="settings-editor">
            <h2>Plugin settings</h2>
            <textarea
              value={settingsJSON}
              onChange={(event) => setSettingsJSON(event.target.value)}
              spellCheck={false}
            />
            <div className="topbar-actions">
              <button type="button" onClick={() => void saveSettings()}>
                Save settings
              </button>
              {settingsStatus ? <span>{settingsStatus}</span> : null}
            </div>
          </div>
        </section>
      ) : null}

      <section className="metrics-grid" aria-label="Stream metrics">
        <Metric icon={<MonitorPlay size={20} />} label="Active streams" value={counts.sessions.active} detail={`${counts.sessions.direct_play} direct, ${counts.sessions.transcoding} transcoding`} />
        <Metric icon={<Server size={20} />} label="Servers" value={counts.servers.total} detail={`${counts.servers.online} online, ${counts.servers.offline} offline`} tone={counts.servers.offline > 0 ? "warn" : "ok"} />
        <Metric icon={<Users size={20} />} label="Users" value={counts.users.unique} detail={`${counts.users.active_today} today, ${counts.users.active_this_week} this week`} />
        <Metric icon={<Clock3 size={20} />} label="Plays" value={counts.history.this_month} detail={`${counts.history.today} today, ${counts.history.total} all time`} />
      </section>

      <section className="map-panel">
        <div className="panel-heading">
          <div>
            <h2>{viewMode === "globe" ? "Global playback" : "Playback map"}</h2>
            <p>{points.length} session{points.length === 1 ? "" : "s"} with coordinates</p>
          </div>
          <div className="status-pill">
            <Circle size={10} fill="currentColor" />
            {lastUpdated ? `Updated ${timeAgo(lastUpdated)}` : "Waiting for data"}
          </div>
        </div>
        {viewMode === "globe" ? (
          <Suspense fallback={<div className="globe-loading">Loading globe...</div>}>
            <GlobeView sessions={points} />
          </Suspense>
        ) : (
          <WorldMap sessions={points} />
        )}
      </section>

      <section className="workspace-grid">
        <div className="main-column">
          <section className="sessions-panel">
            <div className="panel-heading compact">
              <div>
                <h2>Active sessions</h2>
                <p>{activeSessions.length} currently open</p>
              </div>
            </div>
            <SessionList sessions={activeSessions} />
          </section>

          <section className="history-panel">
            <div className="panel-heading compact">
              <div>
                <h2>Realtime playback history</h2>
                <p>{history.total ? `${formatNumber(history.total)} retained plays` : "Waiting for completed plays"}</p>
              </div>
              <div className="status-pill muted">
                <RefreshCw size={14} />
                {history.last_sync_at ? `Synced ${timeAgo(new Date(history.last_sync_at))}` : "Sync pending"}
              </div>
            </div>
            <HistoryList history={history} />
          </section>
        </div>

        <aside className="side-panel">
          <div className="panel-heading compact">
            <div>
              <h2>Server mix</h2>
              <p>By source type</p>
            </div>
          </div>
          <Breakdown rows={Object.entries(counts.servers.by_type)} total={counts.servers.total} />

          <div className="panel-heading compact spacer">
            <div>
              <h2>Media mix</h2>
              <p>Recent history</p>
            </div>
          </div>
          <Breakdown rows={Object.entries(counts.media)} total={counts.history.total} />
        </aside>
      </section>
    </main>
  );
}

function Metric({ icon, label, value, detail, tone }: { icon: React.ReactNode; label: string; value: number; detail: string; tone?: "ok" | "warn" }) {
  return (
    <article className={`metric ${tone ?? ""}`}>
      <div className="metric-icon">{icon}</div>
      <div>
        <span>{label}</span>
        <strong>{formatNumber(value)}</strong>
        <small>{detail}</small>
      </div>
    </article>
  );
}

function SessionList({ sessions }: { sessions: Session[] }) {
  if (!sessions.length) {
    return (
      <div className="empty-state">
        <MonitorPlay size={24} />
        <p>No active playback sessions.</p>
      </div>
    );
  }

  return (
    <div className="session-list">
      {sessions.map((session) => (
        <article className="session-row" key={session.id}>
          <div className="session-main">
            <div className="session-title-row">
              <h3>{displayTitle(session)}</h3>
              <span className={session.is_transcoding ? "badge transcode" : "badge direct"}>
                {session.is_transcoding ? <Cpu size={14} /> : <Activity size={14} />}
                {session.is_transcoding ? "Transcode" : "Direct"}
              </span>
            </div>
            <div className="session-meta">
              <span><Users size={14} />{session.user_name || "Unknown user"}</span>
              <span><Server size={14} />{session.server_name || "Unknown server"}</span>
              <span><Film size={14} />{[session.video_resolution, session.video_codec].filter(Boolean).join(" / ") || session.media_type || "Media"}</span>
              <span><Database size={14} />{session.location || session.ip_address || "No location"}</span>
            </div>
          </div>
          <div className="session-side">
            <strong>{session.player_state || "playing"}</strong>
            <small>{formatDurationSince(session.started_at)}</small>
            <small>{timeAgo(new Date(session.last_seen_at))}</small>
          </div>
        </article>
      ))}
    </div>
  );
}

function HistoryList({ history }: { history: PlaybackHistoryPage }) {
  if (!history.items.length) {
    return (
      <div className="empty-state">
        <Clock3 size={24} />
        <p>No playback history has been synced yet.</p>
      </div>
    );
  }

  return (
    <div className="history-list">
      {history.items.map((item) => (
        <article className="history-row" key={item.session_id}>
          <div className="history-main">
            <div className="session-title-row">
              <h3>{historyTitle(item)}</h3>
              <span className={item.completed ? "badge direct" : "badge partial"}>
                {item.completed ? "Completed" : "Partial"}
              </span>
            </div>
            <div className="session-meta">
              <span><Users size={14} />{item.username || item.profile_name || "Unknown user"}</span>
              <span><Film size={14} />{item.media_type || "Media"}</span>
              <span><Activity size={14} />{item.play_method || "play"}</span>
              <span><Database size={14} />{item.client_ip || "No IP"}</span>
            </div>
          </div>
          <div className="history-side">
            <strong>{formatWatch(item)}</strong>
            <small>{timeAgo(new Date(item.ended_at))}</small>
          </div>
        </article>
      ))}
      <div className="sync-footnote">
        <span><Timer size={14} />{history.synced_rows ? `${history.synced_rows} new` : "Live polling"}</span>
        {history.pruned_rows ? <span>{history.pruned_rows} pruned by retention</span> : null}
      </div>
    </div>
  );
}

function Breakdown({ rows, total }: { rows: [string, number][]; total: number }) {
  if (!rows.length) {
    return <div className="empty-mini">No data</div>;
  }
  return (
    <div className="breakdown">
      {rows.map(([label, value]) => {
        const pct = total > 0 ? Math.max(4, Math.round((value / total) * 100)) : 0;
        return (
          <div className="breakdown-row" key={label || "unknown"}>
            <div>
              <span>{label || "unknown"}</span>
              <strong>{formatNumber(value)}</strong>
            </div>
            <div className="bar">
              <span style={{ width: `${pct}%` }} />
            </div>
          </div>
        );
      })}
    </div>
  );
}

function displayTitle(session: Session) {
  if (session.media_type?.toLowerCase() === "episode" && session.series_name) {
    const episode = session.season_number && session.episode_number ? `S${String(session.season_number).padStart(2, "0")}E${String(session.episode_number).padStart(2, "0")}` : "";
    return [session.series_name, episode, session.episode_title].filter(Boolean).join(" - ");
  }
  return session.media_title || "Untitled media";
}

function historyTitle(item: PlaybackHistoryItem) {
  return item.media_title || item.media_item_id || "Untitled media";
}

function friendlyError(message: string) {
  if (message.includes("not_ready") || message.includes("plugin not configured")) {
    return "Plugin is not configured. Add the plugin database URL and Continuum source database URL in Continuum plugin settings.";
  }
  return message.length > 180 ? `${message.slice(0, 180)}...` : message;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value || 0);
}

function formatWatch(item: PlaybackHistoryItem) {
  const watched = formatDuration(item.watched_seconds || 0);
  if (!item.duration_seconds) return watched;
  return `${watched} / ${formatDuration(item.duration_seconds)}`;
}

function formatDurationSince(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "recent";
  return formatDuration(Math.max(0, (Date.now() - date.getTime()) / 1000));
}

function formatDuration(seconds: number) {
  const total = Math.max(0, Math.round(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = total % 60;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${secs}s`;
  return `${secs}s`;
}

function timeAgo(date: Date) {
  if (Number.isNaN(date.getTime())) return "recently";
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
