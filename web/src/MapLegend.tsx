import { Globe2 } from "lucide-react";
import type { MapSession } from "./types";

export function MapLegend({ sessions }: { sessions: MapSession[] }) {
  if (!sessions.length) {
    return (
      <div className="map-empty">
        <Globe2 size={24} />
        <p>No active sessions have coordinates yet.</p>
      </div>
    );
  }
  return (
    <div className="map-legend">
      {sessions.slice(0, 6).map((session) => (
        <div key={session.session_id}>
          <strong>{session.user_name || "Unknown user"}</strong>
          <span>
            {session.client.location || session.client.ip || "Unknown location"}
            {session.client.source?.startsWith("geoip:") ? ` via ${session.client.source.slice("geoip:".length)}` : ""}
          </span>
        </div>
      ))}
    </div>
  );
}
