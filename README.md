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

Only `username` and `password` are required; `email` may be omitted. Usernames are normalized to lowercase and must be 3–32 ASCII letters, digits, or underscores. Supplied emails are normalized to lowercase and unique. Passwords must contain at least 6 characters and no more than 72 UTF-8 bytes; bcrypt hashes are stored and never returned by the API. Duplicate username or email returns `409`.

### Login

```powershell
curl.exe -i -X POST http://localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' --data-raw '{"identifier":"sky_rider","password":"A-long-unique-passphrase-2026"}'
```

`identifier` accepts the registered username or email address. For older clients, `email` remains accepted instead. The response contains an `access_token` (HS256 JWT), a one-time rotating `refresh_token`, their expiry timestamps, and public user fields. Invalid credentials return a generic `401` response. Configure access-token lifetime with `JWT_EXPIRY` (default `15m`); refresh tokens expire after 30 days and only their SHA-256 hashes are stored server-side.

When the access token is close to expiry, exchange the refresh token at `POST /api/v1/auth/refresh` with `{"refresh_token":"…"}`. The response returns a new access token and a rotated refresh token; the previous refresh token is immediately invalidated. Store both tokens securely and never log or expose the refresh token.

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
{"pilot_name":"sky_rider","start_time":"2026-11-13T10:00:00Z"}
```

The `201` response contains a server-generated `session_id`. Upload batches to `POST /api/v1/tracking/sessions/{session_id}/points` with `{"session_id":"…","trackpoints":[...]}`. Each point requires `lat`, `lon`, `altitude` (meters), `speed` (m/s), and an RFC 3339 `timestamp`; optional fields include vertical speed (m/s), GPS/barometric altitude (meters), heading/bearing (degrees), wind (m/s and degrees), GPS accuracy, satellites, pressure (hPa), and temperature (°C). The response reports the number of newly accepted points; duplicate timestamps are safely ignored.

Stop the session with `POST /api/v1/tracking/sessions/{session_id}/stop` and `{"end_time":"…"}`. Authenticated `GET /api/v1/tracking` lists only the caller's active sessions. Session creation is limited to 10 requests/minute and point batches to 60 requests/minute per client IP.

Authenticated `GET /api/v1/flight-logs` and `/api/v1/chat` remain reserved and return `501 Not Implemented`; Redis client initialization is available for future modules.

## Tests and build

```sh
go test ./...
go build ./cmd/server
```

## Configuration

See `.env.example`. In `development` and `test`, an omitted `DATABASE_DSN` defaults to the local Compose PostgreSQL at `localhost:5432` using the development credentials; set it explicitly when using a different local PostgreSQL instance. Production requires an explicit `DATABASE_DSN`. A `JWT_SECRET` of at least 32 bytes is always required. `CORS_ALLOWED_ORIGINS` is a comma-separated exact allowlist; leave empty for same-origin/native clients. `TRUSTED_PROXIES` must contain only explicit proxy IPs/CIDRs you control; it defaults to trusting no proxies. Registration is limited to 5 requests/minute per client IP and login to 10/minute per client IP. This limiter is process-local, so enforce equivalent limits at the ingress/load balancer in multi-instance deployments. Never commit `.env` or production credentials.
