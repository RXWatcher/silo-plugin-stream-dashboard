export type Counts = {
  servers: { total: number; online: number; offline: number; by_type: Record<string, number> };
  sessions: { active: number; transcoding: number; direct_play: number };
  history: { total: number; today: number; this_week: number; this_month: number };
  users: { unique: number; active_today: number; active_this_week: number };
  media: Record<string, number>;
};

export type Session = {
  id: string;
  server_name: string;
  server_type: string;
  user_name: string;
  media_title: string;
  media_type: string;
  series_name?: string;
  season_number?: number;
  episode_number?: number;
  episode_title?: string;
  is_transcoding: boolean;
  progress: number;
  player_state?: string;
  device_name?: string;
  client_name?: string;
  video_resolution?: string;
  video_codec?: string;
  audio_codec?: string;
  transcode_video_codec?: string;
  transcode_reason?: string;
  ip_address?: string;
  location?: string;
  cdn_node_ip?: string;
  cdn_node_location?: string;
  is_local: boolean;
  started_at: string;
  last_seen_at: string;
  vm_status?: string;
  cpu_usage?: number;
  memory_usage?: number;
  allocated_cpus?: number;
  allocated_ram_gb?: number;
  network_traffic_in?: string;
  network_traffic_out?: string;
};

export type Endpoint = {
  name?: string;
  ip?: string;
  location?: string;
  lat?: number;
  lon?: number;
  source?: string;
};

export type MapSession = {
  session_id: string;
  user_name: string;
  media_title: string;
  server_name: string;
  client: Endpoint;
  cdn?: Endpoint;
  server?: Endpoint;
};

export type PlaybackHistoryItem = {
  session_id: string;
  user_id: number;
  username: string;
  profile_id: string;
  profile_name?: string;
  media_item_id: string;
  media_file_id: number;
  media_title: string;
  media_type: string;
  play_method: string;
  started_at: string;
  ended_at: string;
  watched_seconds: number;
  duration_seconds?: number;
  completed: boolean;
  client_ip?: string;
  created_at: string;
};

export type PlaybackHistoryPage = {
  items: PlaybackHistoryItem[];
  total: number;
  limit: number;
  offset: number;
  synced_rows: number;
  pruned_rows: number;
  last_sync_at?: string;
};

export type Overview = {
  counts: Counts;
  sessions: Session[];
  map_sessions: MapSession[];
  history: PlaybackHistoryPage;
  refresh_seconds: number;
  generated_at: string;
};
