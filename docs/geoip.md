# GeoIP Enrichment

Operator notes for the GeoIP layer (`internal/geoip/geoip.go`). The map and globe views are downstream consumers; the locator itself is a small lookup interface with provider fallback and an in-memory TTL cache.

## When Enrichment Runs

GeoIP only runs inside `Store.MapSessions` (per session, per request). It does **not** run on `Sessions()` / `Counts()` / history queries. That means:

- The active sessions table shows whatever `Location` / `geo_lat` / `geo_lon` the source DB has — unenriched.
- The map view enriches client IPs (always) and CDN node IPs (if `geoip_lookup_cdn_nodes=true`).
- The server endpoint on the map line uses `default_server_lat` / `default_server_lon` — **never** GeoIP.

Two switches control enrichment behavior:

| Flag | Default | Meaning |
| --- | --- | --- |
| `geoip_lookup_missing_coordinates` | true | If the source row has no coords, run GeoIP. |
| `geoip_override_session_coordinates` | false | If the source already has coords, replace them with GeoIP. Off by default — trust the database first. |

For CDN endpoints, `force=true` is passed to `enrichGeoIP` so the lookup runs regardless of `geoip_lookup_missing_coordinates`. Pre-existing CDN coords are still respected unless `override=true`.

## Provider Model

Four providers, plug-in style:

| Name | Type | Auth | Default base URL |
| --- | --- | --- | --- |
| `mmdb` | Local MaxMind GeoIP2 City DB (`oschwald/geoip2-golang`) | n/a (filesystem) | `geoip_database_path` |
| `ipapi` | HTTP — ip-api.com free tier | None | `http://ip-api.com/json` |
| `ipinfo` | HTTP — ipinfo.io | `geoip_ipinfo_token` (optional) | `https://api.ipinfo.io/lookup` |
| `ipwhois` | HTTP — ipwho.is | None | `https://ipwho.is` |

A provider is **available** only if its enable flag is set (`geoip_ipapi_enabled`, `geoip_ipinfo_enabled`, `geoip_ipwhois_enabled`) or for `mmdb` if `geoip_database_path` resolves to a readable file. If `geoip_enabled=true` but no provider is configured, `Open` returns an error and `Configure` fails — the plugin keeps running with the previous pools/locator.

The order in `geoip_provider_order` (default `mmdb,ipapi,ipinfo,ipwhois`) is applied in that sequence. Anything available but not listed in the order is appended at the end. Names are normalized: `local`/`maxmind` -> `mmdb`, `ip-api`/`ip_api` -> `ipapi`, `ipwhois.io` -> `ipwhois`, etc. (`normalizeProviderName`).

### Lookup short-circuit

`Locator.Lookup` walks providers in order and returns the **first** one that yields `(ok=true, err=nil)`. A provider that errors silently moves on. Negative results are cached too — see below.

The returned `source` is `"geoip:<providerName>"`. That string is visible in `/api/map` responses as `client.source` / `cdn.source`, which is the single best signal of which provider answered.

## MMDB Vs HTTP — Tradeoffs

The MMDB provider is preferred for production:

- Zero per-request latency, no rate limits, no external network dependency.
- Predictable accuracy — if the DB ages out, every dot is consistently wrong rather than randomly wrong.
- Privacy — no client IPs leaked to third parties.

Caveats:

- Requires a license (MaxMind GeoLite2 is free, GeoIP2 is paid).
- Returns `(0, 0)` for unknown IPs; the provider treats that as not-found and falls through to the next provider.
- The plugin does not auto-refresh the file. To update, replace the file at `geoip_database_path` and reconfigure (which calls `Open` again and reopens the DB).

HTTP providers (`ipapi`, `ipinfo`, `ipwhois`) are mostly useful when you can't ship an MMDB:

- All three have free tiers with low rate limits. ip-api free is ~45 req/min; ipinfo free is 50k/month; ipwho.is is ~10k/month.
- Latency is the per-provider HTTP timeout (`geoip_request_timeout_seconds`, floor 1, default 3). The plugin uses a single `http.Client` per locator, so the timeout is enforced at the client level.
- The context has its own deadline applied via `context.WithTimeout(ctx, l.timeout)` — providers don't get to exceed it individually, they share the budget. If the first provider takes the full 3 s, the second has none left.

Rule of thumb: enable MMDB even if you also enable an HTTP provider — order it first so 99% of lookups never hit the network.

## The Cache

In-memory map, RW-mutex guarded. Keys are IP text (no normalization beyond trim/parse).

- `geoip_cache_ttl_seconds` — floor 60, default 3600. Entries expire after TTL whether positive or negative.
- `geoip_cache_max_entries` — floor 100, default 4096. When full, the cache prunes expired entries first; if still full, evicts the entry with the **earliest expiry** (effectively LRU-by-creation, since all entries get the same TTL).
- **Negative caching is on**: a failed lookup (`ok=false` from every provider) is cached too. This prevents hammering ip-api with the same unresolvable IP every 30 s. The flip side: if a provider was transiently down, the failure is sticky for the cache TTL.

There is no admin endpoint to flush the cache. The only ways to clear it:

- Wait for TTL expiry.
- Reconfigure the plugin (the old `Locator` is closed and a new one with empty cache is created).
- Restart the plugin process.

## Private IPs

`isPrivateOrLocal` rejects RFC1918, loopback, link-local, unspecified, and multicast addresses **before** any provider is called. `geoip_include_private_ips=true` flips this off and lets private IPs through to providers — useful in a LAN-only deployment with a local MMDB that has private ranges populated, but generally not what you want.

## Source Field Reference

On `/api/map`, `client.source` / `cdn.source` will be one of:

| Value | Meaning |
| --- | --- |
| `database` | Source DB already had coords; GeoIP was not consulted (only when `geoip_override_session_coordinates=false`). |
| `geoip:mmdb` | Resolved by the local MMDB file. |
| `geoip:ipapi` | Resolved by ip-api.com. |
| `geoip:ipinfo` | Resolved by ipinfo.io. |
| `geoip:ipwhois` | Resolved by ipwho.is. |
| `config` | Server endpoint only — taken from `default_server_lat/lon`. |
| _empty_ | No coordinates resolved. For client endpoints, the row is dropped from `/api/map` entirely. |

## Debugging Provider Issues

1. Pick a known public IP from `/api/sessions`. Confirm GeoIP is enabled and the IP isn't private.
2. Call `/api/map` and find the matching row. Inspect `client.source` — that tells you which provider answered (or the absence of the row tells you all providers failed).
3. To check a specific HTTP provider out-of-band, hit the same URL the plugin would: e.g. `curl 'http://ip-api.com/json/8.8.8.8?fields=status,message,country,regionName,city,lat,lon,query'`. The plugin sets only an `Accept: application/json` header; it does not set User-Agent.
4. If the same IP returns different coordinates across requests, you're hitting different providers due to transient failures upstream — pin the order and reduce the timeout so the slow one drops out cleanly.
5. To check the MMDB file is being read, set `geoip_provider_order=mmdb` and disable the HTTP providers, then check `/api/map` — every row should now have `source: "geoip:mmdb"` or be missing entirely.

## Gotchas

- `record.Location.Latitude == 0 && record.Location.Longitude == 0` is treated as "not found" by the MMDB provider. Real coords near (0,0) (the Gulf of Guinea) will be misclassified — unlikely in practice but worth knowing.
- ipinfo's free response shape varies. The plugin tolerates both the structured `geo.{latitude,longitude}` form and the legacy `loc: "lat,lon"` string.
- The provider HTTP client has no retries. A 5xx from one provider falls through to the next, but that consumes the shared request timeout.
- `Locator.Lookup` returns `(0, 0, "", "", false)` for an empty/invalid IP without calling any provider. Look for that signature in logs to distinguish "didn't try" from "tried and failed".
