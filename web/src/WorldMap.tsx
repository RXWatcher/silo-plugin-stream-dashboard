import { MapLegend } from "./MapLegend";
import { hasCoords, project } from "./geo";
import type { MapSession } from "./types";

export default function WorldMap({ sessions }: { sessions: MapSession[] }) {
  return (
    <div className="world-map" role="img" aria-label="World map with active playback sessions">
      <svg viewBox="0 0 1000 500" preserveAspectRatio="xMidYMid meet">
        <rect width="1000" height="500" rx="12" />
        <path d="M154 144h126l38 43-37 51 22 54-77 32-92-19-37-76 31-54zm246-40 132 9 63 49-12 69-94 15-83-42-50-55zm224 39 140-9 96 66-24 93-116 30-104-46-18-78zM210 354l116 5 60 45-46 55-112-12-64-48zm313-40 116 22 53 69-31 68-129-7-64-79z" />
        <path className="latitude" d="M0 250h1000M0 125h1000M0 375h1000M250 0v500M500 0v500M750 0v500" />
        {sessions.map((session) => {
          const client = project(session.client.lat!, session.client.lon!);
          const server = session.server && hasCoords(session.server) ? project(session.server.lat!, session.server.lon!) : null;
          return (
            <g key={session.session_id}>
              {server ? <line className="route" x1={server.x} y1={server.y} x2={client.x} y2={client.y} /> : null}
              <circle className="server-dot" cx={server?.x ?? client.x} cy={server?.y ?? client.y} r="5" />
              <circle className="client-pulse" cx={client.x} cy={client.y} r="10" />
              <circle className="client-dot" cx={client.x} cy={client.y} r="5" />
            </g>
          );
        })}
      </svg>
      <MapLegend sessions={sessions} />
    </div>
  );
}
