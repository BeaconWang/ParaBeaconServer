# ParaBeacon Server

Go 1.22+ API starter for paragliding, hang gliding, and general aviation sports. Includes PostgreSQL/PostGIS, optional Redis, UUID user accounts, bcrypt passwords, and short-lived JWT bearer authentication.

## Requirements

- Go 1.22+
- Docker Compose (recommended for local PostgreSQL/PostGIS and Redis)

## Run locally

1. Start the development dependencies:

   ```sh
   docker compose up -d
   ```

2. Copy `.env.example` to `.env` and replace `JWT_SECRET` with a cryptographically random value of at least 32 bytes. For example:

   ```sh
   openssl rand -base64 48
   ```

   The PostgreSQL credentials in the Compose file are for local development only. Use managed secrets and TLS-enabled database/Redis connections in deployed environments.

3. Download dependencies and start the server:

   ```sh
   go mod tidy
   go run ./cmd/server
   ```

   The application applies the initial GORM schema migration at startup. For production releases, use versioned database migrations and a controlled migration process rather than relying on automatic schema changes.

The API listens on `http://localhost:8080`. Redis is optional when `REDIS_URL` is empty; when configured, it must be reachable at startup. The development Compose file enables PostGIS on first database initialization.

## Authentication and database design

The client connects over HTTP/JSON. Requests pass through the Gin router and rate limiter, then the auth handler/service/repository layers. PostgreSQL stores accounts in `users`: UUID primary key, normalized unique `username`, optional unique `email` (SQL `NULL` when omitted), bcrypt `password_hash`, and creation/update timestamps. Only username and password are required at registration; email is optional. Passwords are never stored or returned in plaintext. JWT access tokens are stateless and short-lived; protected API calls send them as `Authorization: Bearer <token>`. The existing `User` model is migrated at startup to allow accounts without email.

## API

### Register

```powershell
curl.exe -i -X POST http://localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' --data-raw '{"username":"sky_rider","password":"A-long-unique-passphrase-2026"}'
```

Only `username` and `password` are required; `email` may be omitted. Usernames are stored exactly as registered (casing is preserved) and must be 3–32 ASCII letters, digits, or underscores; names that differ only by case are treated as duplicates. Supplied emails are normalized to lowercase and unique. Passwords must contain at least 6 characters and no more than 72 UTF-8 bytes; bcrypt hashes are stored and never returned by the API. Duplicate username or email returns `409`.

### Login

```powershell
curl.exe -i -X POST http://localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' --data-raw '{"identifier":"sky_rider","password":"A-long-unique-passphrase-2026"}'
```

`identifier` accepts the registered username or email address and matches case-insensitively (the stored account name keeps its original casing). For older clients, `email` remains accepted instead. The response contains an `access_token` (HS256 JWT), a one-time rotating `refresh_token`, their expiry timestamps, and public user fields. Invalid credentials return a generic `401` response. Configure access-token lifetime with `JWT_EXPIRY` (default `15m`); refresh tokens have a rolling 60-day idle lifetime and only their SHA-256 hashes are stored server-side.

When the access token is close to expiry, exchange the refresh token at `POST /api/v1/auth/refresh` with `{"refresh_token":"…"}`. A successful refresh within 60 days returns a new access token and a rotated refresh token with a new 60-day expiry; the previous refresh token is immediately invalidated. The session expires if its current refresh token is unused for more than 60 days. Store both tokens securely and never log or expose the refresh token. On sign-out, send the current refresh token to `POST /api/v1/auth/logout` to revoke it.

### Authenticated profile

Pass the login token as a bearer token:

```powershell
curl.exe -i http://localhost:8080/api/v1/user/profile -H 'Authorization: Bearer YOUR_ACCESS_TOKEN'
```

### Health checks

- `GET /healthz`: process liveness.
- `GET /readyz`: PostgreSQL readiness.

### Live flight tracking

The authenticated tracking API accepts live flight telemetry over HTTPS JSON. The server derives the owner from the Bearer token; clients cannot write to another user's session. Sessions and points are stored in `live_track_sessions` and `live_track_points`. Point uploads are idempotent per session and timestamp, and each batch is limited to 100 points.

Create a session when a flight becomes active:

```json
POST /api/v1/tracking/sessions
{"pilot_name":"sky_rider","start_time":"2026-11-13T10:00:00Z","publicly_share":false}
```

The `201` response contains a server-generated `session_id`. Public location sharing is opt-in and defaults to `false`; an opted-in active session exposes only the pilot name, latest location, optional heading, and observation time to authenticated map users. The owner can immediately revoke consent with `PATCH /api/v1/tracking/sessions/{session_id}/public-sharing` and `{"publicly_share":false}`. A pilot that goes offline keeps sharing its last known location and flight trail (`is_online=false`) until the owner revokes consent or deletes the data with `DELETE /api/v1/tracking/location-data`; only then is the retained location history removed from the server.

Upload batches to `POST /api/v1/tracking/sessions/{session_id}/points` with `{"session_id":"…","trackpoints":[...]}`. Each point requires `lat`, `lon`, `altitude` (meters), `speed` (m/s), and an RFC 3339 `timestamp`; optional fields include vertical speed (m/s), GPS/barometric altitude (meters), heading/bearing (degrees), wind (m/s and degrees), GPS accuracy, satellites, pressure (hPa), and temperature (°C). The response reports the number of newly accepted points; duplicate timestamps are safely ignored.

Fetch public pilot locations within a WGS-84 viewport using public or authenticated `GET /api/v1/map/active-locations?west=…&south=…&east=…&north=…`. Bounds may cross the antimeridian. Offline pilots are included by default with `is_online=false`; pass `show_offline=false` to return only pilots that reported a location within the last 90 seconds. Responses are cursor-paged (`locations`, `next_cursor`) so clients can load every matching pilot; results are ordered by session ID and contain no account IDs or private flight telemetry. Anonymous requests include all opted-in sessions; authenticated requests also include the caller's own opted-in session. Stop the session with `POST /api/v1/tracking/sessions/{session_id}/stop` and `{"end_time":"…"}`. Authenticated `GET /api/v1/tracking` lists only the caller's active sessions. Session creation is limited to 10 requests/minute, public-sharing updates to 20, point batches to 60, and map viewport queries to 60 requests/minute per client IP.

### Web pages

Open `/` for the ParaBeacon home page. It provides the primary navigation, account sign-in, and short guides to the live map and release console. `/home` and `/home/` serve the same page. The account widget is also available on the other web pages; it keeps only the short-lived access token in `sessionStorage` for the current browser tab and does not store a refresh token. Home and live pages default to the browser language when no preference exists and provide a manual English/Chinese switch; the selected language is saved under `parabeacon-language`.

Open `/live` for the public full-screen map; signing in is optional. It loads every cursor page of publicly shared active pilots and refreshes the map every 15 seconds. The panel shows each returned pilot name, coordinates, heading when available, observation time, and freshness status; the page intentionally does not expose altitude or other private telemetry because the public API contract excludes those fields. The authenticated public-map API also includes the signed-in user's own opted-in session; anonymous requests include all opted-in sessions.

Authenticated `GET /api/v1/flight-logs` returns the signed-in account's paginated records. Snapshot synchronization uses `POST /api/v1/flight-logs/syncs` to begin, one binary `PUT /api/v1/flight-logs/syncs/{sync_id}/flights/{index}` with `Content-Type: application/zip` per flight, then `POST /api/v1/flight-logs/syncs/{sync_id}/commit`. Only commit replaces the account's live records; incomplete uploads remain staged and expire. `/api/v1/chat` remains reserved and returns `501 Not Implemented`.

## Android APK releases

APK releases are stored as immutable files under `APK_STORAGE_DIR` (default `./data/apks`) and indexed in the `app_releases` table by Android `versionCode`. Mount that directory on persistent storage in production. Set `APK_PACKAGE_NAME` to the Android application ID. Public clients can query `GET /api/v1/app/releases/latest` for the newest build or `GET /api/v1/app/releases` for every published version. Every published version is publicly downloadable through its `download_url` at `GET /api/v1/app/releases/{version_code}/download`; downloads are served as APK attachments and include a SHA-256 digest, byte size, and an atomic download count for client verification and statistics. The `/release` page, release metadata APIs, and APK downloads all send no-store cache headers so clients request current server data.

## Flight archives

Flight synchronization stores one maximally compressed `.flight.zip` file per flight under `FLIGHT_LOG_STORAGE_DIR` (default `./data/flights`). The server generates each archive GUID and stores it with the account/flight metadata in `flight_logs`. Mount this directory on persistent storage in production; the database alone is not sufficient to restore synchronized flight records.

The `Beacon` account is the root account: the server grants it every permission regardless of group membership. APK release management for other accounts requires the `apk_management` permission. The startup seed creates the `Beacon administrators` group with all built-in permissions and assigns existing `Beacon` accounts (case-insensitive) to it for compatibility. Sign in through `POST /api/v1/auth/login`, then send the returned bearer token when publishing. Upload a signed APK directly as the request body:

```sh
curl -X POST http://localhost:8080/api/v1/admin/app/releases \
  -H 'Authorization: Bearer YOUR_BEACON_ACCESS_TOKEN' \
  -H 'X-APK-Version: 1.2.3' \
  -H 'X-APK-Version-Code: 123' \
  -H 'Content-Type: application/vnd.android.package-archive' \
  --data-binary @build/app/outputs/flutter-apk/app-release.apk
```

The server limits uploads to 512 MiB, verifies the APK archive contains `AndroidManifest.xml`, records its digest, generated storage key, Markdown release notes, and download count, and refuses duplicate version codes. The web console sends `multipart/form-data` with `version_name`, `version_code`, `notes`, and `apk`; the raw binary header-based upload remains supported for API clients. Android verifies the APK signing certificate when the user installs it; every release must be signed with the same production signing key and use a strictly increasing `versionCode`. The server uses a ten-minute HTTP body/read timeout to support APK transfer; keep equivalent or longer proxy timeouts at the ingress.

### Release management web UI

Open `/release` to use the embedded release management console. Sign in with an account assigned to a group containing `apk_management` to publish or delete releases. The console supports signed APK upload, public release history, SHA-256 inspection, downloads, multi-select deletion, and English/Chinese language switching. APK contents are immutable after publishing. Deleting a release permanently unlinks its APK file from server storage and removes its public metadata; the account-protected endpoint is `DELETE /api/v1/admin/app/releases/{version_code}`.

### Account groups and permissions

Open `/groups` with the root `Beacon` account or an account assigned to `account_group_management` to create groups, assign permissions, and assign accounts to groups. Built-in permissions are `apk_management` for APK release management, `xc_route_customization` for XC competition route customization, and `account_group_management` for account-group administration. Permissions are resolved from PostgreSQL on every protected request; access tokens do not carry an authoritative permission snapshot. The group APIs are `GET/POST /api/v1/admin/account-groups`, `PUT /api/v1/admin/account-groups/{group_id}/permissions`, and `PUT /api/v1/admin/accounts/{user_id}/groups`. The current repository does not yet expose XC competition route entities; `xc_route_customization` is seeded and ready for that feature. The API reports an error rather than treating a failed file removal as a successful deletion.

## Tests and build

```sh
go test ./...
go build ./cmd/server
```

## Configuration

See `.env.example`. In `development` and `test`, an omitted `DATABASE_DSN` defaults to the local Compose PostgreSQL at `localhost:5432` using the development credentials; set it explicitly when using a different local PostgreSQL instance. Production requires an explicit `DATABASE_DSN`. A `JWT_SECRET` of at least 32 bytes is always required. `CORS_ALLOWED_ORIGINS` is a comma-separated exact allowlist; leave empty for same-origin/native clients. `TRUSTED_PROXIES` must contain only explicit proxy IPs/CIDRs you control; it defaults to trusting no proxies. Registration is limited to 5 requests/minute per client IP and login to 10/minute per client IP. This limiter is process-local, so enforce equivalent limits at the ingress/load balancer in multi-instance deployments. Never commit `.env` or production credentials.
