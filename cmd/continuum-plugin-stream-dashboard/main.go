package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	goruntime "runtime"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/jackc/pgx/v5/pgxpool"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	publicmanifest "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/runtime"

	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/geoip"
	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/httproutes"
	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/poll"
	pluginrt "github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/runtime"
	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/server"
	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/store"
	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/web"
)

//go:embed manifest.json
var manifestRaw []byte

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "continuum-plugin-stream-dashboard"})
	manifest, err := loadManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}

	httpSrv := httproutes.NewServer()
	scheduled := poll.New()
	var poolPtr atomic.Pointer[pgxpool.Pool]
	var sourcePoolPtr atomic.Pointer[pgxpool.Pool]
	var geoPtr atomic.Pointer[geoip.Locator]

	rt := pluginrt.New(manifest, func(cfg pluginrt.Config) error {
		ctx := context.Background()
		pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("parse database_url: %w", err)
		}
		if pcfg.MaxConns < 4 {
			pcfg.MaxConns = 4
		}
		pool, err := pgxpool.NewWithConfig(ctx, pcfg)
		if err != nil {
			return fmt.Errorf("connect server-manager database: %w", err)
		}
		if err := store.Migrate(ctx, pool); err != nil {
			pool.Close()
			return fmt.Errorf("migrate plugin schema: %w", err)
		}
		appCfg, err := store.New(pool, store.Config{}).ImportLegacyAppConfig(ctx, cfg)
		if err != nil {
			pool.Close()
			return fmt.Errorf("import app config: %w", err)
		}
		appCfg.DatabaseURL = cfg.DatabaseURL
		appCfg.SourceDatabaseURL = cfg.SourceDatabaseURL
		cfg = appCfg
		sourceCfg, err := pgxpool.ParseConfig(cfg.SourceDatabaseURL)
		if err != nil {
			pool.Close()
			return fmt.Errorf("parse source_database_url: %w", err)
		}
		if sourceCfg.MaxConns < 4 {
			sourceCfg.MaxConns = 4
		}
		sourcePool, err := pgxpool.NewWithConfig(ctx, sourceCfg)
		if err != nil {
			pool.Close()
			return fmt.Errorf("connect continuum source database: %w", err)
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			sourcePool.Close()
			return fmt.Errorf("ping plugin database: %w", err)
		}
		if err := sourcePool.Ping(ctx); err != nil {
			pool.Close()
			sourcePool.Close()
			return fmt.Errorf("ping continuum source database: %w", err)
		}
		var locator *geoip.Locator
		if cfg.GeoIPEnabled {
			locator, err = geoip.Open(geoip.Config{
				DatabasePath:    cfg.GeoIPDatabasePath,
				IncludePrivate:  cfg.GeoIPIncludePrivateIPs,
				Timeout:         time.Duration(cfg.GeoIPRequestTimeoutSeconds) * time.Second,
				CacheTTL:        time.Duration(cfg.GeoIPCacheTTLSeconds) * time.Second,
				MaxCacheEntries: cfg.GeoIPCacheMaxEntries,
				ProviderOrder:   cfg.GeoIPProviderOrder,
				IPAPIEnabled:    cfg.GeoIPIPAPIEnabled,
				IPAPIBaseURL:    cfg.GeoIPIPAPIBaseURL,
				IPInfoEnabled:   cfg.GeoIPIPInfoEnabled,
				IPInfoBaseURL:   cfg.GeoIPIPInfoBaseURL,
				IPInfoToken:     cfg.GeoIPIPInfoToken,
				IPWhoisEnabled:  cfg.GeoIPIPWhoisEnabled,
				IPWhoisBaseURL:  cfg.GeoIPIPWhoisBaseURL,
			})
			if err != nil {
				pool.Close()
				sourcePool.Close()
				return fmt.Errorf("open geoip database: %w", err)
			}
		}
		st := store.New(pool, store.Config{
			SourcePool:                   sourcePool,
			DefaultServerLat:             cfg.DefaultServerLat,
			DefaultServerLon:             cfg.DefaultServerLon,
			GeoLocator:                   locator,
			GeoIPLookupMissingCoordinate: cfg.GeoIPLookupMissingCoordinate,
			GeoIPOverrideCoordinates:     cfg.GeoIPOverrideCoordinates,
			GeoIPLookupCDN:               cfg.GeoIPLookupCDN,
		})
		policy := store.RetentionPolicy{
			Days:            cfg.HistoryRetentionDays,
			MaxRows:         cfg.HistoryRetentionMaxRows,
			MinWatchSeconds: cfg.HistoryRetentionMinSeconds,
			CompletedOnly:   cfg.HistoryRetentionCompletedOnly,
		}
		scheduled.Set(st, policy)
		httpSrv.SetHandler(server.New(server.Deps{
			Store:                  st,
			Logger:                 logger,
			WebFS:                  web.FSEmbed(),
			RefreshSeconds:         cfg.RefreshSeconds,
			HistoryRetentionPolicy: policy,
		}))
		if old := poolPtr.Swap(pool); old != nil {
			old.Close()
		}
		if old := sourcePoolPtr.Swap(sourcePool); old != nil {
			old.Close()
		}
		if old := geoPtr.Swap(locator); old != nil {
			old.Close()
		}
		logger.Info("configured stream-dashboard plugin")
		return nil
	})

	sdkruntime.Serve(sdkruntime.ServeConfig{
		Logger: logger,
		Servers: sdkruntime.CapabilityServers{
			Runtime:       rt,
			HttpRoutes:    httpSrv,
			ScheduledTask: scheduled,
		},
	})
}

func loadManifest() (*pluginv1.PluginManifest, error) {
	manifest, err := publicmanifest.Load(manifestRaw)
	if err != nil {
		return nil, fmt.Errorf("load embedded manifest: %w", err)
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	binaryData, err := os.ReadFile(executablePath)
	if err != nil {
		return nil, fmt.Errorf("read executable %q: %w", executablePath, err)
	}
	checksum := sha256.Sum256(binaryData)
	manifest.Checksum = hex.EncodeToString(checksum[:])
	if len(manifest.GetSupportedPlatforms()) == 0 {
		manifest.SupportedPlatforms = []*pluginv1.SupportedPlatform{{Os: goruntime.GOOS, Arch: goruntime.GOARCH}}
	}
	return manifest, nil
}
