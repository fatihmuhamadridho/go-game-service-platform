# Game Service Platform — Admin API

Backend Admin Authentication and Game Management Sprint 1, built in Go with MVC boundaries and MongoDB.

## Run

1. Copy `.env.example` to `.env` and set MongoDB URI, signing secret, and exact dashboard origin. For local browser testing, keep `CORS_ALLOWED_ORIGINS=http://localhost:3000` aligned with dashboard URL and use `AUTH_ALLOW_INSECURE_HTTP=true`; this option only accepts non-HTTPS requests from loopback clients. For a different frontend port, change that origin exactly.
2. Use MongoDB Atlas or a replica set. Session creation and refresh rotation use MongoDB transactions.
3. Start API with `go run ./cmd/api`.

Endpoints:

- `POST /api/admin/auth/v1/login`
- `POST /api/admin/auth/v1/refresh-token`
- `POST /api/admin/auth/v1/logout`
- `GET /api/admin/games/v1/list`
- `GET /api/admin/games/v1/detail/{gameId}`
- `POST /api/admin/games/v1/create`
- `PATCH /api/admin/games/v1/update/{gameId}`
- `DELETE /api/admin/games/v1/delete/{gameId}`
- `GET /api/admin/roles/v1/list`
- `GET /api/admin/roles/v1/detail/{roleId}`
- `POST /api/admin/roles/v1/create`
- `PUT /api/admin/roles/v1/update/{roleId}`
- `DELETE /api/admin/roles/v1/delete/{roleId}`
- `GET /api/admin/admin-users/v1/list`
- `GET /api/admin/admin-users/v1/detail/{adminId}`
- `GET /api/games/v1/list`
- `GET /api/games/v1/popular`

The service creates required indexes at startup. Production requires HTTPS because auth cookies are `Secure` and use the `__Host-` prefix. Local-only `AUTH_ALLOW_INSECURE_HTTP=true` permits non-HTTPS requests from loopback clients and uses non-Secure, `SameSite=Lax` cookies without the `__Host-` prefix; it can connect to local MongoDB or Atlas. Non-loopback requests still require HTTPS. Behind a TLS-terminating proxy, keep the option false, set `TRUSTED_PROXY_CIDRS`, and forward `X-Forwarded-Proto: https` plus the client chain in `X-Forwarded-For`; untrusted forwarding headers are ignored.

## Bootstrap the initial Super Admin

The API never creates a default administrator. Install Bash and MongoDB Shell (`mongosh`) and make sure both are on `PATH`. Generate an Argon2id hash with `go run ./internal/utils/hash-password` in a trusted terminal; password input is hidden and confirmed. Inject the resulting encoded hash and account attributes as environment variables, then run `bash scripts/seed-admin.sh`. The shell seeder reads MongoDB and admin settings from the process environment or local `.env`, creates the required indexes, and atomically seeds the system permission catalog, immutable role, account, credential, assignment, and Super Admin grants. To add or repair the permission catalog and Super Admin grants without changing account credentials, run `bash scripts/seed-admin.sh --permissions-only`.

Required seed variables: `MONGODB_URL`, optional `MONGODB_DATABASE` (derived from the URI path when omitted), `ADMIN_ACCOUNT_ID` (`admin_` prefix), `ADMIN_USERNAME`, `ADMIN_NAME`, `ADMIN_EMAIL`, and `ADMIN_PASSWORD_HASH`. Seeding is transactional and idempotent. It refuses conflicting or modified system-role/account records and never changes an existing password hash. Unit validation is covered by `go test ./...`; live transaction behavior can be exercised with `MONGODB_TEST_URL` pointed at a disposable replica set.

Do not place raw passwords or signing keys in command-line arguments, source control, shell history, or logs. Keep the generated hash in secret-managed environment injection while seeding.

## MVC layout

- `cmd/api`: API entrypoint and application wiring.
- `internal/auth`: Authentication handler, service, MongoDB repository, models, DTOs, password hashing, and JWT/session handling.
- `internal/game`: Admin game catalog DTOs, handler, service, MongoDB model access, indexes, and rate limits.
- `internal/role`: Admin role CRUD handler, validation/service, transactional MongoDB role and permission-grant access, and current permission-key responses.
- `internal/adminaccount`: Read-only admin account directory with safe projections and active-role resolution.
- `internal/publicgame`: Public catalog list and popular-game query handlers with safe projections.
- `internal/order`: Reserved MVC module skeleton for order features.
- `internal/platform/database`: MongoDB client connection and database platform setup.
- `internal/config`: Runtime configuration validation.
- `internal/utils`: Shared helpers; `hash-password` generates Argon2id hashes for controlled bootstrap.

## Security behavior

- Access JWT lifetime: 15 minutes. Opaque refresh token lifetime: 7 days.
- Only refresh/CSRF token digests persist in MongoDB. Refresh generations rotate atomically; replay revokes the whole family.
- Current account status, active role, and granted permissions are read from MongoDB at each authenticated request. The seeder assigns the nine Sprint 1 permission records to `SUPER_ADMIN`; admin routes check those grants like other roles.
- Login and refresh limit each source IP to 20 requests in 15 minutes. Login allows 5 failed authentication attempts per trimmed lowercase identifier in 15 minutes. Rejections include `Retry-After`.
- Rate-limit keys store SHA-256 digests instead of raw identifiers or IP addresses. Set `TRUSTED_PROXY_CIDRS` only to proxy networks you operate; forwarded client IP headers from other peers are ignored.
- Auth responses use `Cache-Control: no-store`; logs include method, route, status, and duration only.
- Admin game routes require current `game.read/create/update/delete` grants for every role, including system Super Admin. All game routes enforce exact Origin, CSRF on reads and writes, and per-minute IP/admin limits. Soft-deleted games stay reserved for 60 days; public query helpers include only active, non-deleted records.
- Admin role routes require current `role.read/create/update/delete` permission, exact Origin and CSRF even on reads, and read/mutation rate limits. Role and permission-grant mutations use MongoDB transactions; soft-deleted roles keep grants and assignment history for platform-managed retention.
- Admin account directory routes require the current `admin-account.read` grant, exact Origin, CSRF even on reads, and per-admin/IP throttles. Responses contain profile fields and one active role; inconsistent role assignments fail closed.
- Public catalog routes return active, non-deleted games only and use a public projection. They allow non-credentialed cross-origin GETs and apply a 1,200 requests/IP/minute rolling limit.
- Set `MONGODB_TEST_URL` to a replica-set URI to run the MongoDB integration test.

## BE-TASKS coverage

| Task | Implementation and verification |
| --- | --- |
| BE-AUTH-001 | `internal/auth/handler.go` JWT/session middleware; `repository.go` resolves account and current role/permissions per request; `a_test_handler_test.go` covers current permission, revoked session behavior through the repository integration test, and safe auth errors. |
| BE-AUTH-002 | `AuthService.Login` validates and normalizes identifier; Mongo lookup uses case-insensitive collation and requires active account, credential, and exactly one active role; tests cover trimmed email lookup, generic credential failure, invalid input, and Argon2id. |
| BE-AUTH-003 | CORS exact-origin and preflight checks in handler; Mongo rolling-window IP and identifier rate limits; tests cover rejected/missing Origin, credentialed CORS, preflight, throttling, and `Retry-After`. |
| BE-AUTH-004 | Token/cookie issuance in `service.go`, `token.go`, and `handler.go`; session stores only token digests; tests verify JWT claims, opaque token, cookie attributes, and no-store response behavior. |
| BE-AUTH-005 | `RotateSession` transaction rotates generations and revokes replayed families; Mongo integration test covers rotation, replay, and family revocation. |
| BE-AUTH-006 | Logout is idempotent, validates CSRF for an active family, revokes that family, and clears both cookies only after successful store operation; handler tests cover repeated logout and store failure. |
| BE-AUTH-007 | `scripts/seed-admin.sh` uses MongoDB Shell to bootstrap one immutable `SUPER_ADMIN`, active account, Argon2id credential, role assignment, and permission grants; `RevokeAccountSessions` is the account offboarding hook. |
| BE-AUTH-008 | `EnsureIndexes` and `scripts/seed-admin.sh` create ERD unique, collation, partial, family/account, and TTL indexes; repository test files cover index definitions and Mongo integration behavior. |
| BE-GAME-001 | Game routes reuse current session state, require exact `game.*` permission, enforce CSRF and Origin, and permit publication flags only for system Super Admins; auth/game handler tests cover permission, CSRF, and CORS methods. |
| BE-GAME-002 | Admin list validates IFA query parameters, searches name/slug, filters active/popular state, excludes tombstones, and uses stable sort and pagination; handler and Mongo integration tests cover behavior. |
| BE-GAME-003 | Detail validates canonical `game_` ULID IDs and hides tombstones; model and repository tests cover ID and visibility behavior. |
| BE-GAME-004 | Create validates and normalizes catalog fields, applies inactive defaults, allocates unique sort order, and maps unique conflicts; service and Mongo integration tests cover defaults, conflicts, and concurrency. |
| BE-GAME-005 | Update applies only supplied mutable fields, rejects publication changes by other admins, enforces final state/uniqueness, and skips writes for no-op changes. |
| BE-GAME-006 | Delete atomically sets tombstone and clears publication flags; repeated delete is idempotent and retained data remains unavailable to readers. |
| BE-GAME-007 | Mongo unique/collation/query/TTL indexes, rolling one-minute rate limits, public visibility query, and safe error mapping are covered by unit and optional Mongo integration tests. |

## BE-ROLE coverage

| Task | Implementation |
| --- | --- |
| BE-ROLE-001 | `scripts/seed-admin.sh` seeds the nine system permission keys idempotently and preserves the immutable system Super Admin role. |
| BE-ROLE-002 | Role routes reuse current auth-session resolution and require explicit current permission grants, including for Super Admin; exact-Origin CORS and CSRF validation apply to read and write requests. |
| BE-ROLE-003 | Role list validates IFA query parameters, searches name/code case-insensitively, filters status, sorts deterministically, paginates, and returns permission keys. |
| BE-ROLE-004 | Role detail validates `role_` IDs and returns only active, non-deleted roles with current permission keys. |
| BE-ROLE-005 | Role create normalizes fields, validates active catalog permissions, and inserts role plus grants in one MongoDB transaction. |
| BE-ROLE-006 | Role update preserves omitted fields, replaces supplied grants atomically, protects system roles, blocks deactivation with active assignments, and skips no-op writes. |
| BE-ROLE-007 | Role delete soft-deletes custom roles, rejects active assignments/system role, and treats retained tombstones idempotently. |
| BE-ROLE-008 | Startup configures role, grant, permission, and assignment indexes; role endpoints apply per-minute IP/admin throttles and safe status mapping. |

## BE-ADMIN coverage

| Task | Implementation |
| --- | --- |
| BE-ADMIN-001 | Shared session middleware, current `admin-account.read` permission, exact-Origin CORS, CSRF validation, and a projection that excludes credentials and session data. |
| BE-ADMIN-002 | List filters search/status/roleId and pagination, joins the active role, excludes deleted accounts, and sorts by username then account ID. |
| BE-ADMIN-003 | Detail validates `admin_` identifiers, returns profile and active role, and maps absent/deleted accounts to 404. |
| BE-ADMIN-004 | Mongo aggregation validates the single-active-role invariant; existing account/assignment/role indexes support joins; shared rolling limits enforce 600/admin and 1,200/IP per minute. |
| BE-ADMIN-005 | Invalid, unauthenticated, forbidden, missing, throttled, and technical failures map to safe `USR` envelopes. |

## BE-PUBGAME coverage

| Task | Implementation |
| --- | --- |
| BE-PUBGAME-001 | Public list supports documented filters, search, sorting and pagination, while ignoring unknown parameters and `Accept-Language`. |
| BE-PUBGAME-002 | List returns an approved public projection of active, non-deleted games and pagination metadata. |
| BE-PUBGAME-003 | Popular list constrains visibility to active, popular and non-deleted games with documented pagination. |
| BE-PUBGAME-004 | Public query index, rolling IP limit, `Retry-After`, and documented error envelopes are wired at startup. |

## Verify

Run `go test ./...` and `go build ./...`. Set `MONGODB_TEST_URL` to a disposable MongoDB URI to enable MongoDB integration tests; without it, those tests are skipped.
