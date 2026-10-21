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

## API

### Register

```powershell
curl.exe -i -X POST http://localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' --data-raw '{"username":"sky_rider","email":"pilot@example.com","password":"A-long-unique-passphrase-2026"}'
```

Usernames are normalized to lowercase and must be 3–32 ASCII letters, digits, or underscores. Emails are normalized to lowercase and unique. Passwords must be 12–72 bytes; bcrypt hashes are stored and never returned by the API. Duplicate username/email returns `409`.

### Login

```powershell
curl.exe -i -X POST http://localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' --data-raw '{"email":"pilot@example.com","password":"A-long-unique-passphrase-2026"}'
```

The response contains an `access_token` (HS256 JWT), `token_type`, `expires_at`, and public user fields. Invalid credentials return a generic `401` response. Configure token lifetime with `JWT_EXPIRY` (default `15m`).

### Authenticated profile

Pass the login token as a bearer token:

```powershell
curl.exe -i http://localhost:8080/api/v1/user/profile -H 'Authorization: Bearer YOUR_ACCESS_TOKEN'
```

### Health checks

- `GET /healthz`: process liveness.
- `GET /readyz`: PostgreSQL readiness.

### Reserved modules

Authenticated `GET /api/v1/tracking`, `/api/v1/flight-logs`, and `/api/v1/chat` return `501 Not Implemented`. They are route placeholders for later WebSocket/MQTT tracking, IGC storage, and IM work; no transport or domain logic is implied yet. Redis client initialization is already available for those modules.

## Tests and build

```sh
go test ./...
go build ./cmd/server
```

## Configuration

See `.env.example`. `DATABASE_DSN` and a `JWT_SECRET` of at least 32 bytes are required. `CORS_ALLOWED_ORIGINS` is a comma-separated exact allowlist; leave empty for same-origin/native clients. `TRUSTED_PROXIES` must contain only explicit proxy IPs/CIDRs you control; it defaults to trusting no proxies. Registration is limited to 5 requests/minute per client IP and login to 10/minute per client IP. This limiter is process-local, so enforce equivalent limits at the ingress/load balancer in multi-instance deployments. Never commit `.env` or production credentials.
