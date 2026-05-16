package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool                         *pgxpool.Pool
	sourcePool                   *pgxpool.Pool
	defaultServerLat             float64
	defaultServerLon             float64
	geoLocator                   GeoLocator
	geoIPLookupMissingCoordinate bool
	geoIPOverrideCoordinates     bool
	geoIPLookupCDN               bool
	historySyncMu                sync.Mutex
	lastRealtimeHistorySync      time.Time
}

const (
	historyBatchLimit           = 5000
	historyScheduledMaxBatches  = 20
	historyRealtimeMaxBatches   = 1
	historyRealtimeSyncInterval = 10 * time.Second
	historyCursorOverlap        = 2 * time.Minute
)

type Config struct {
	SourcePool                   *pgxpool.Pool
	DefaultServerLat             float64
	DefaultServerLon             float64
	GeoLocator                   GeoLocator
	GeoIPLookupMissingCoordinate bool
	GeoIPOverrideCoordinates     bool
	GeoIPLookupCDN               bool
}

type RetentionPolicy struct {
	Days            int
	MaxRows         int
	MinWatchSeconds int
	CompletedOnly   bool
}

type GeoLocator interface {
	Lookup(ctx context.Context, ip string) (lat float64, lon float64, location string, source string, ok bool)
}

type Session struct {
	ID                  string    `json:"id"`
	ServerID            int       `json:"server_id"`
	ServerName          string    `json:"server_name"`
	ServerType          string    `json:"server_type"`
	UserName            string    `json:"user_name"`
	MediaTitle          string    `json:"media_title"`
	MediaType           string    `json:"media_type"`
	SeriesName          string    `json:"series_name,omitempty"`
	SeasonNumber        *int      `json:"season_number,omitempty"`
	EpisodeNumber       *int      `json:"episode_number,omitempty"`
	EpisodeTitle        string    `json:"episode_title,omitempty"`
	IsTranscoding       bool      `json:"is_transcoding"`
	Progress            int       `json:"progress"`
	PlayerState         string    `json:"player_state,omitempty"`
	DeviceName          string    `json:"device_name,omitempty"`
	ClientName          string    `json:"client_name,omitempty"`
	VideoResolution     string    `json:"video_resolution,omitempty"`
	VideoCodec          string    `json:"video_codec,omitempty"`
	AudioCodec          string    `json:"audio_codec,omitempty"`
	TranscodeVideoCodec string    `json:"transcode_video_codec,omitempty"`
	TranscodeReason     string    `json:"transcode_reason,omitempty"`
	IPAddress           string    `json:"ip_address,omitempty"`
	Location            string    `json:"location,omitempty"`
	GeoLat              *float64  `json:"geo_lat,omitempty"`
	GeoLon              *float64  `json:"geo_lon,omitempty"`
	CDNNodeIP           string    `json:"cdn_node_ip,omitempty"`
	CDNNodeLocation     string    `json:"cdn_node_location,omitempty"`
	IsLocal             bool      `json:"is_local"`
	StartedAt           time.Time `json:"started_at"`
	LastSeenAt          time.Time `json:"last_seen_at"`
	VMID                *int      `json:"vm_id,omitempty"`
	VMStatus            string    `json:"vm_status,omitempty"`
	CPUUsage            *float64  `json:"cpu_usage,omitempty"`
	MemoryUsage         *float64  `json:"memory_usage,omitempty"`
	AllocatedCPUs       *int      `json:"allocated_cpus,omitempty"`
	AllocatedRAMGB      *float64  `json:"allocated_ram_gb,omitempty"`
	NetworkTrafficIn    string    `json:"network_traffic_in,omitempty"`
	NetworkTrafficOut   string    `json:"network_traffic_out,omitempty"`
}

type PlaybackHistoryItem struct {
	SessionID       string    `json:"session_id"`
	UserID          int       `json:"user_id"`
	Username        string    `json:"username"`
	ProfileID       string    `json:"profile_id"`
	ProfileName     string    `json:"profile_name,omitempty"`
	MediaItemID     string    `json:"media_item_id"`
	MediaFileID     int       `json:"media_file_id"`
	MediaTitle      string    `json:"media_title"`
	MediaType       string    `json:"media_type"`
	PlayMethod      string    `json:"play_method"`
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	WatchedSeconds  float64   `json:"watched_seconds"`
	DurationSeconds *float64  `json:"duration_seconds,omitempty"`
	Completed       bool      `json:"completed"`
	ClientIP        string    `json:"client_ip,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type PlaybackHistoryPage struct {
	Items      []PlaybackHistoryItem `json:"items"`
	Total      int                   `json:"total"`
	Limit      int                   `json:"limit"`
	Offset     int                   `json:"offset"`
	SyncedRows int                   `json:"synced_rows"`
	PrunedRows int                   `json:"pruned_rows"`
	LastSyncAt *time.Time            `json:"last_sync_at,omitempty"`
}

type ServerSummary struct {
	Total   int            `json:"total"`
	Online  int            `json:"online"`
	Offline int            `json:"offline"`
	ByType  map[string]int `json:"by_type"`
}

type Counts struct {
	Servers  ServerSummary  `json:"servers"`
	Sessions SessionCounts  `json:"sessions"`
	History  HistoryCounts  `json:"history"`
	Users    UserCounts     `json:"users"`
	Media    map[string]int `json:"media"`
}

type SessionCounts struct {
	Active      int `json:"active"`
	Transcoding int `json:"transcoding"`
	DirectPlay  int `json:"direct_play"`
}

type HistoryCounts struct {
	Total     int `json:"total"`
	Today     int `json:"today"`
	ThisWeek  int `json:"this_week"`
	ThisMonth int `json:"this_month"`
}

type UserCounts struct {
	Unique         int `json:"unique"`
	ActiveToday    int `json:"active_today"`
	ActiveThisWeek int `json:"active_this_week"`
}

type MapSession struct {
	SessionID  string       `json:"session_id"`
	UserName   string       `json:"user_name"`
	MediaTitle string       `json:"media_title"`
	ServerName string       `json:"server_name"`
	Client     MapEndpoint  `json:"client"`
	CDN        *MapEndpoint `json:"cdn,omitempty"`
	Server     *MapEndpoint `json:"server,omitempty"`
}

type MapEndpoint struct {
	Name     string   `json:"name,omitempty"`
	IP       string   `json:"ip,omitempty"`
	Location string   `json:"location,omitempty"`
	Lat      *float64 `json:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty"`
	Source   string   `json:"source,omitempty"`
}

func New(pool *pgxpool.Pool, cfg Config) *Store {
	return &Store{
		pool:                         pool,
		sourcePool:                   cfg.SourcePool,
		defaultServerLat:             cfg.DefaultServerLat,
		defaultServerLon:             cfg.DefaultServerLon,
		geoLocator:                   cfg.GeoLocator,
		geoIPLookupMissingCoordinate: cfg.GeoIPLookupMissingCoordinate,
		geoIPOverrideCoordinates:     cfg.GeoIPOverrideCoordinates,
		geoIPLookupCDN:               cfg.GeoIPLookupCDN,
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) PingSource(ctx context.Context) error {
	if s.sourcePool == nil {
		return nil
	}
	return s.sourcePool.Ping(ctx)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS playback_history (
	session_id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL DEFAULT 0,
	username TEXT NOT NULL DEFAULT '',
	profile_id TEXT NOT NULL DEFAULT '',
	profile_name TEXT NOT NULL DEFAULT '',
	media_item_id TEXT NOT NULL DEFAULT '',
	media_file_id INTEGER NOT NULL DEFAULT 0,
	media_title TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
	play_method TEXT NOT NULL DEFAULT '',
	started_at TIMESTAMPTZ NOT NULL,
	ended_at TIMESTAMPTZ NOT NULL,
	watched_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
	duration_seconds DOUBLE PRECISION,
	completed BOOLEAN NOT NULL DEFAULT FALSE,
	client_ip TEXT NOT NULL DEFAULT '',
	source_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS playback_history_ended_idx ON playback_history (ended_at DESC);
CREATE INDEX IF NOT EXISTS playback_history_user_ended_idx ON playback_history (user_id, ended_at DESC);
CREATE INDEX IF NOT EXISTS playback_history_media_type_idx ON playback_history (media_type);
CREATE TABLE IF NOT EXISTS sync_state (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`)
	return err
}

func (s *Store) Sessions(ctx context.Context) ([]Session, error) {
	pool := s.source()
	rows, err := pool.Query(ctx, `
SELECT
	s.session_id,
	0,
	COALESCE(NULLIF(s.reporting_node, ''), 'Continuum'),
	'continuum',
	COALESCE(u.username, ''),
	COALESCE(ep.title, mi.title, ''),
	COALESCE(CASE WHEN ep.content_id IS NOT NULL THEN 'episode' ELSE mi.type END, ''),
	COALESCE(series.title, ''),
	NULL::integer,
	NULL::integer,
	COALESCE(ep.title, ''),
	(s.play_method = 'transcode' OR s.transcode_audio),
	0,
	CASE WHEN s.is_paused THEN 'paused' ELSE 'playing' END,
	'',
	'Continuum',
	COALESCE(s.target_resolution, mf.resolution, ''),
	COALESCE(s.target_video_codec, mf.codec_video, ''),
	COALESCE(s.target_audio_codec, mf.codec_audio, ''),
	COALESCE(s.target_video_codec, ''),
	'',
	COALESCE(HOST(s.client_ip), ''),
	'',
	NULL::double precision,
	NULL::double precision,
	'',
	'',
	FALSE,
	s.started_at,
	s.updated_at,
	NULL::integer,
	'',
	NULL::double precision,
	NULL::double precision,
	NULL::integer,
	NULL::double precision,
	'',
	''
FROM public.playback_sessions_sync s
LEFT JOIN public.users u ON u.id = s.user_id
LEFT JOIN public.media_files mf ON mf.id = s.media_file_id
LEFT JOIN public.media_items mi ON mi.content_id = mf.content_id
LEFT JOIN public.episodes ep ON ep.content_id = mf.episode_id
LEFT JOIN public.media_items series ON series.content_id = ep.series_id
ORDER BY s.started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var row Session
		if err := rows.Scan(&row.ID, &row.ServerID, &row.ServerName, &row.ServerType,
			&row.UserName, &row.MediaTitle, &row.MediaType,
			&row.SeriesName, &row.SeasonNumber, &row.EpisodeNumber, &row.EpisodeTitle,
			&row.IsTranscoding, &row.Progress, &row.PlayerState,
			&row.DeviceName, &row.ClientName,
			&row.VideoResolution, &row.VideoCodec, &row.AudioCodec,
			&row.TranscodeVideoCodec, &row.TranscodeReason,
			&row.IPAddress, &row.Location, &row.GeoLat, &row.GeoLon,
			&row.CDNNodeIP, &row.CDNNodeLocation, &row.IsLocal,
			&row.StartedAt, &row.LastSeenAt,
			&row.VMID, &row.VMStatus, &row.CPUUsage, &row.MemoryUsage,
			&row.AllocatedCPUs, &row.AllocatedRAMGB, &row.NetworkTrafficIn, &row.NetworkTrafficOut); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) source() *pgxpool.Pool {
	if s.sourcePool != nil {
		return s.sourcePool
	}
	return s.pool
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var out Counts
	out.Servers.ByType = map[string]int{}
	out.Media = map[string]int{}

	source := s.source()
	if err := source.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE enabled), COUNT(*) FILTER (WHERE NOT enabled) FROM public.stream_nodes`).Scan(&out.Servers.Total, &out.Servers.Online, &out.Servers.Offline); err != nil {
		_ = source.QueryRow(ctx, `SELECT 1, 1, 0`).Scan(&out.Servers.Total, &out.Servers.Online, &out.Servers.Offline)
	}
	typeRows, err := source.Query(ctx, `SELECT 'continuum', COUNT(*) FROM public.playback_sessions_sync`)
	if err != nil {
		return out, err
	}
	for typeRows.Next() {
		var typ string
		var count int
		if err := typeRows.Scan(&typ, &count); err != nil {
			typeRows.Close()
			return out, err
		}
		out.Servers.ByType[typ] = count
	}
	typeRows.Close()

	if err := source.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE play_method = 'transcode' OR transcode_audio), COUNT(*) FILTER (WHERE NOT (play_method = 'transcode' OR transcode_audio)) FROM public.playback_sessions_sync`).Scan(&out.Sessions.Active, &out.Sessions.Transcoding, &out.Sessions.DirectPlay); err != nil {
		return out, err
	}
	if err := s.pool.QueryRow(ctx, `
SELECT
	COUNT(*),
	COUNT(*) FILTER (WHERE ended_at >= NOW() - INTERVAL '24 hours'),
	COUNT(*) FILTER (WHERE ended_at >= NOW() - INTERVAL '7 days'),
	COUNT(*) FILTER (WHERE ended_at >= NOW() - INTERVAL '30 days')
FROM playback_history`).Scan(&out.History.Total, &out.History.Today, &out.History.ThisWeek, &out.History.ThisMonth); err != nil {
		return out, err
	}
	if err := s.pool.QueryRow(ctx, `
SELECT
	COUNT(DISTINCT username),
	COUNT(DISTINCT username) FILTER (WHERE ended_at >= NOW() - INTERVAL '24 hours'),
	COUNT(DISTINCT username) FILTER (WHERE ended_at >= NOW() - INTERVAL '7 days')
FROM playback_history
WHERE username <> ''`).Scan(&out.Users.Unique, &out.Users.ActiveToday, &out.Users.ActiveThisWeek); err != nil {
		return out, err
	}
	mediaRows, err := s.pool.Query(ctx, `SELECT media_type, COUNT(*) FROM playback_history GROUP BY media_type ORDER BY COUNT(*) DESC LIMIT 12`)
	if err != nil {
		return out, err
	}
	defer mediaRows.Close()
	for mediaRows.Next() {
		var mediaType string
		var count int
		if err := mediaRows.Scan(&mediaType, &count); err != nil {
			return out, err
		}
		out.Media[mediaType] = count
	}
	return out, mediaRows.Err()
}

func (s *Store) MapSessions(ctx context.Context) ([]MapSession, error) {
	sessions, err := s.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MapSession, 0, len(sessions))
	for _, session := range sessions {
		client := MapEndpoint{
			IP:       session.IPAddress,
			Location: session.Location,
			Lat:      session.GeoLat,
			Lon:      session.GeoLon,
		}
		if client.Lat != nil && client.Lon != nil {
			client.Source = "database"
		}
		client = s.enrichGeoIP(ctx, client, session.IPAddress, false)
		if client.Lat == nil || client.Lon == nil {
			continue
		}
		item := MapSession{
			SessionID:  session.ID,
			UserName:   session.UserName,
			MediaTitle: session.MediaTitle,
			ServerName: session.ServerName,
			Client:     client,
		}
		if session.CDNNodeIP != "" {
			cdn := MapEndpoint{IP: session.CDNNodeIP, Location: session.CDNNodeLocation}
			if s.geoIPLookupCDN {
				cdn = s.enrichGeoIP(ctx, cdn, session.CDNNodeIP, true)
			}
			item.CDN = &cdn
		}
		if session.ServerName != "" {
			lat, lon := s.defaultServerLat, s.defaultServerLon
			item.Server = &MapEndpoint{Name: session.ServerName, Lat: &lat, Lon: &lon, Source: "config"}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) SyncPlaybackHistory(ctx context.Context, policy RetentionPolicy) (int, int, error) {
	return s.syncPlaybackHistory(ctx, policy, historyScheduledMaxBatches, false)
}

func (s *Store) SyncPlaybackHistoryRealtime(ctx context.Context, policy RetentionPolicy) (int, int, error) {
	return s.syncPlaybackHistory(ctx, policy, historyRealtimeMaxBatches, true)
}

func (s *Store) syncPlaybackHistory(ctx context.Context, policy RetentionPolicy, maxBatches int, realtime bool) (int, int, error) {
	if maxBatches < 1 {
		maxBatches = 1
	}
	if realtime {
		if !s.historySyncMu.TryLock() {
			return 0, 0, nil
		}
		defer s.historySyncMu.Unlock()
		if !s.lastRealtimeHistorySync.IsZero() && time.Since(s.lastRealtimeHistorySync) < historyRealtimeSyncInterval {
			return 0, 0, nil
		}
		s.lastRealtimeHistorySync = time.Now()
	} else {
		s.historySyncMu.Lock()
		defer s.historySyncMu.Unlock()
	}

	source := s.source()
	cursorEndedAt, cursorSessionID, err := s.historyCursor(ctx)
	if err != nil {
		return 0, 0, err
	}
	queryEndedAt, querySessionID := cursorEndedAt, cursorSessionID
	if queryEndedAt.After(historyEpoch()) {
		queryEndedAt = queryEndedAt.Add(-historyCursorOverlap)
		querySessionID = ""
	}
	synced := 0
	for batch := 0; batch < maxBatches; batch++ {
		rows, err := source.Query(ctx, `
SELECT
	h.session_id,
	h.user_id,
	COALESCE(u.username, ''),
	h.profile_id,
	COALESCE(NULLIF(h.profile_name, ''), h.profile_id),
	h.media_item_id,
	h.media_file_id,
	COALESCE(ep.title, mi.title, ''),
	COALESCE(CASE WHEN ep.content_id IS NOT NULL THEN 'episode' ELSE mi.type END, ''),
	h.play_method,
	h.started_at,
	h.ended_at,
	h.watched_seconds,
	h.duration_seconds,
	h.completed,
	COALESCE(HOST(h.client_ip), '')
FROM public.playback_history_admin h
LEFT JOIN public.users u ON u.id = h.user_id
LEFT JOIN public.media_items mi ON mi.content_id = h.media_item_id
LEFT JOIN public.episodes ep ON ep.content_id = h.media_item_id
WHERE (h.ended_at, h.session_id) > ($1, $2)
ORDER BY h.ended_at ASC, h.session_id ASC
LIMIT $3`, queryEndedAt, querySessionID, historyBatchLimit)
		if err != nil {
			return synced, 0, err
		}

		batchRows := 0
		for rows.Next() {
			var item PlaybackHistoryItem
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.Username, &item.ProfileID, &item.ProfileName,
				&item.MediaItemID, &item.MediaFileID, &item.MediaTitle, &item.MediaType, &item.PlayMethod,
				&item.StartedAt, &item.EndedAt, &item.WatchedSeconds, &item.DurationSeconds, &item.Completed, &item.ClientIP); err != nil {
				rows.Close()
				return synced, 0, err
			}
			tag, err := s.pool.Exec(ctx, `
INSERT INTO playback_history (
	session_id, user_id, username, profile_id, profile_name, media_item_id, media_file_id,
	media_title, media_type, play_method, started_at, ended_at, watched_seconds,
	duration_seconds, completed, client_ip, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NOW())
ON CONFLICT (session_id) DO NOTHING`,
				item.SessionID, item.UserID, item.Username, item.ProfileID, item.ProfileName, item.MediaItemID, item.MediaFileID,
				item.MediaTitle, item.MediaType, item.PlayMethod, item.StartedAt, item.EndedAt, item.WatchedSeconds,
				item.DurationSeconds, item.Completed, item.ClientIP)
			if err != nil {
				rows.Close()
				return synced, 0, err
			}
			synced += int(tag.RowsAffected())
			queryEndedAt = item.EndedAt
			querySessionID = item.SessionID
			if afterHistoryCursor(item.EndedAt, item.SessionID, cursorEndedAt, cursorSessionID) {
				cursorEndedAt = item.EndedAt
				cursorSessionID = item.SessionID
			}
			batchRows++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return synced, 0, err
		}
		rows.Close()
		if batchRows == 0 {
			break
		}
		if err := s.setHistoryCursor(ctx, cursorEndedAt, cursorSessionID); err != nil {
			return synced, 0, err
		}
		if batchRows < historyBatchLimit {
			break
		}
	}
	if err := s.setSyncState(ctx, "history_last_sync_at", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return synced, 0, err
	}
	pruned, err := s.ApplyRetention(ctx, policy)
	return synced, pruned, err
}

func afterHistoryCursor(endedAt time.Time, sessionID string, cursorEndedAt time.Time, cursorSessionID string) bool {
	return endedAt.After(cursorEndedAt) || endedAt.Equal(cursorEndedAt) && sessionID > cursorSessionID
}

func (s *Store) historyCursor(ctx context.Context) (time.Time, string, error) {
	endedText, endedOK, err := s.syncState(ctx, "history_cursor_ended_at")
	if err != nil {
		return time.Time{}, "", err
	}
	sessionID, sessionOK, err := s.syncState(ctx, "history_cursor_session_id")
	if err != nil {
		return time.Time{}, "", err
	}
	if endedOK && sessionOK {
		endedAt, err := time.Parse(time.RFC3339Nano, endedText)
		if err != nil {
			return time.Time{}, "", err
		}
		return endedAt, sessionID, nil
	}
	var since time.Time
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(ended_at), '1970-01-01'::timestamptz) FROM playback_history`).Scan(&since); err != nil {
		return time.Time{}, "", err
	}
	return since, "", nil
}

func historyEpoch() time.Time {
	return time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
}

func (s *Store) setHistoryCursor(ctx context.Context, endedAt time.Time, sessionID string) error {
	if err := s.setSyncState(ctx, "history_cursor_ended_at", endedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return s.setSyncState(ctx, "history_cursor_session_id", sessionID)
}

func (s *Store) syncState(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM sync_state WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) setSyncState(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO sync_state (key, value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, key, value)
	return err
}

func (s *Store) ApplyRetention(ctx context.Context, policy RetentionPolicy) (int, error) {
	total := 0
	if policy.MinWatchSeconds > 0 {
		tag, err := s.pool.Exec(ctx, `DELETE FROM playback_history WHERE watched_seconds < $1`, policy.MinWatchSeconds)
		if err != nil {
			return total, err
		}
		total += int(tag.RowsAffected())
	}
	if policy.CompletedOnly {
		tag, err := s.pool.Exec(ctx, `DELETE FROM playback_history WHERE completed = FALSE`)
		if err != nil {
			return total, err
		}
		total += int(tag.RowsAffected())
	}
	if policy.Days > 0 {
		tag, err := s.pool.Exec(ctx, `DELETE FROM playback_history WHERE ended_at < NOW() - ($1::text || ' days')::interval`, policy.Days)
		if err != nil {
			return total, err
		}
		total += int(tag.RowsAffected())
	}
	if policy.MaxRows > 0 {
		tag, err := s.pool.Exec(ctx, `
DELETE FROM playback_history
WHERE session_id IN (
	SELECT session_id FROM playback_history
	ORDER BY ended_at DESC
	OFFSET $1
)`, policy.MaxRows)
		if err != nil {
			return total, err
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

func (s *Store) PlaybackHistory(ctx context.Context, limit, offset int, policy RetentionPolicy, realtime bool) (PlaybackHistoryPage, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	synced, pruned := 0, 0
	var err error
	if realtime {
		synced, pruned, err = s.SyncPlaybackHistoryRealtime(ctx, policy)
	} else {
		synced, pruned, err = s.SyncPlaybackHistory(ctx, policy)
	}
	if err != nil {
		return PlaybackHistoryPage{}, err
	}
	lastSync, err := s.LastHistorySyncAt(ctx)
	if err != nil {
		return PlaybackHistoryPage{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM playback_history`).Scan(&total); err != nil {
		return PlaybackHistoryPage{}, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT session_id, user_id, username, profile_id, profile_name, media_item_id, media_file_id,
	media_title, media_type, play_method, started_at, ended_at, watched_seconds,
	duration_seconds, completed, client_ip, created_at
FROM playback_history
ORDER BY ended_at DESC
LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return PlaybackHistoryPage{}, err
	}
	defer rows.Close()
	items := []PlaybackHistoryItem{}
	for rows.Next() {
		var item PlaybackHistoryItem
		if err := rows.Scan(&item.SessionID, &item.UserID, &item.Username, &item.ProfileID, &item.ProfileName,
			&item.MediaItemID, &item.MediaFileID, &item.MediaTitle, &item.MediaType, &item.PlayMethod,
			&item.StartedAt, &item.EndedAt, &item.WatchedSeconds, &item.DurationSeconds, &item.Completed, &item.ClientIP, &item.CreatedAt); err != nil {
			return PlaybackHistoryPage{}, err
		}
		items = append(items, item)
	}
	return PlaybackHistoryPage{Items: items, Total: total, Limit: limit, Offset: offset, SyncedRows: synced, PrunedRows: pruned, LastSyncAt: lastSync}, rows.Err()
}

func (s *Store) LastHistorySyncAt(ctx context.Context) (*time.Time, error) {
	value, ok, err := s.syncState(ctx, "history_last_sync_at")
	if err != nil || !ok {
		return nil, err
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, nil
	}
	return &ts, nil
}

func (s *Store) enrichGeoIP(ctx context.Context, endpoint MapEndpoint, ip string, force bool) MapEndpoint {
	if s.geoLocator == nil || ip == "" {
		return endpoint
	}
	hasCoordinates := endpoint.Lat != nil && endpoint.Lon != nil
	if !force && hasCoordinates && !s.geoIPOverrideCoordinates {
		return endpoint
	}
	if !force && !hasCoordinates && !s.geoIPLookupMissingCoordinate {
		return endpoint
	}
	geoLat, geoLon, geoLocation, geoSource, ok := s.geoLocator.Lookup(ctx, ip)
	if !ok {
		return endpoint
	}
	lat, lon := geoLat, geoLon
	endpoint.Lat = &lat
	endpoint.Lon = &lon
	endpoint.Source = geoSource
	if endpoint.Location == "" || s.geoIPOverrideCoordinates {
		endpoint.Location = geoLocation
	}
	return endpoint
}

func (s *Store) Status(ctx context.Context) (map[string]any, error) {
	var dbNow time.Time
	if err := s.pool.QueryRow(ctx, `SELECT NOW()`).Scan(&dbNow); err != nil {
		return nil, err
	}
	counts, err := s.Counts(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"database_time": dbNow,
		"active":        true,
		"sessions":      counts.Sessions.Active,
		"servers":       counts.Servers.Total,
	}, nil
}

func FormatMediaTitle(s Session) string {
	if strings.EqualFold(s.MediaType, "episode") && s.SeriesName != "" {
		parts := []string{s.SeriesName}
		if s.SeasonNumber != nil && s.EpisodeNumber != nil {
			parts = append(parts, fmt.Sprintf("S%02dE%02d", *s.SeasonNumber, *s.EpisodeNumber))
		}
		if s.EpisodeTitle != "" {
			parts = append(parts, s.EpisodeTitle)
		}
		return strings.Join(parts, " · ")
	}
	return s.MediaTitle
}
