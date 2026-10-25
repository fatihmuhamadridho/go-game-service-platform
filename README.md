# Game Service Platform — Admin Authentication

Backend Admin Authentication Sprint 1, built in Go with MVC boundaries and MongoDB.

## Run

1. Copy `.env.example` to `.env` and set MongoDB URI, signing secret, and exact dashboard origin. For local browser testing, keep `CORS_ALLOWED_ORIGINS=http://localhost:3000` aligned with dashboard URL and use `AUTH_ALLOW_INSECURE_HTTP=true`; this option only accepts non-HTTPS requests from loopback clients. For a different frontend port, change that origin exactly.
2. Use MongoDB Atlas or a replica set. Session creation and refresh rotation use MongoDB transactions.
3. Start API with `go run ./cmd/api`.

Endpoints:

- `POST /api/admin/auth/v1/login`
- `POST /api/admin/auth/v1/refresh-token`
- `POST /api/admin/auth/v1/logout`

The service creates required indexes at startup. Production requires HTTPS because auth cookies are `Secure` and use the `__Host-` prefix. Local-only `AUTH_ALLOW_INSECURE_HTTP=true` permits non-HTTPS requests from loopback clients and uses non-Secure, `SameSite=Lax` cookies without the `__Host-` prefix; it can connect to local MongoDB or Atlas. Non-loopback requests still require HTTPS. Behind a TLS-terminating proxy, keep the option false, set `TRUSTED_PROXY_CIDRS`, and forward `X-Forwarded-Proto: https` plus the client chain in `X-Forwarded-For`; untrusted forwarding headers are ignored.

## Bootstrap the initial Super Admin

The API never creates a default administrator. Generate an Argon2id hash by running `go run ./internal/utils/hash-password` in a trusted terminal; password input is hidden and confirmed. Pass the resulting encoded hash and account attributes to `scripts/seed-admin.js` using a secret manager or protected environment injection, then run `mongosh --nodb --file scripts/seed-admin.js`. The script creates its own MongoDB connection from injected `MONGODB_URL`.

Required seed variables: `MONGODB_URL`, optional `MONGODB_DATABASE`, `ADMIN_ACCOUNT_ID` (`admin_` prefix), `ADMIN_USERNAME`, `ADMIN_NAME`, `ADMIN_EMAIL`, and `ADMIN_PASSWORD_HASH`. Seed script is transactional and idempotent. It refuses conflicting or modified system-role/account records and never changes an existing password hash.

Do not place raw passwords or signing keys in command-line arguments, source control, shell history, or logs. Keep the generated hash in secret-managed environment injection while seeding.

## MVC layout

- `cmd/api`: API entrypoint and application wiring.
- `internal/auth`: Authentication handler, service, MongoDB repository, models, DTOs, password hashing, and JWT/session handling.
- `internal/game`: Reserved MVC module skeleton for game features.
- `internal/order`: Reserved MVC module skeleton for order features.
- `internal/platform/database`: MongoDB client connection and database platform setup.
- `internal/config`: Runtime configuration validation.
- `internal/utils`: Shared helpers; `hash-password` generates Argon2id hashes for controlled bootstrap.

## Security behavior

- Access JWT lifetime: 15 minutes. Opaque refresh token lifetime: 7 days.
- Only refresh/CSRF token digests persist in MongoDB. Refresh generations rotate atomically; replay revokes the whole family.
- Current account status, active role, and permissions are read from MongoDB at each authenticated request. `SUPER_ADMIN` receives all active permission keys.
- Login and refresh limit each source IP to 20 requests in 15 minutes. Login allows 5 failed authentication attempts per trimmed lowercase identifier in 15 minutes. Rejections include `Retry-After`.
- Rate-limit keys store SHA-256 digests instead of raw identifiers or IP addresses. Set `TRUSTED_PROXY_CIDRS` only to proxy networks you operate; forwarded client IP headers from other peers are ignored.
- Auth responses use `Cache-Control: no-store`; logs include method, route, status, and duration only.
- Set `MONGODB_TEST_URL` to a replica-set URI to run the MongoDB integration test.

## Verify

Run `go test ./...` and `go build ./...`.
