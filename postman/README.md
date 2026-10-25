# Postman: Admin API

## Use the Git-native workspace

The collection is under `collections/admin-authentication/` and the shareable local environment is `environments/local.environment.yaml`. Open the repository as a Postman Native Git workspace, then select **Game Service Platform - Local** and set `identifier` and `password` for an account created with `go run ./cmd/seed-admin` or provisioned separately. Keep credentials in Postman's local secret storage; do not commit them. For private per-machine overrides, use a file such as `environments/local.private.environment.yaml`; `.gitignore` excludes private environment files and generated Postman run output.

## Run

1. Start API with `go run ./cmd/api` or `air`.
2. Open the **Auth** folder and run **Login**. Postman stores auth cookies and the request script saves `csrfToken`.
3. Open the **Game Management** folder and run List, Detail, Create, Update, or Delete. The folder reuses Login's cookies and `csrfToken`.
4. Open **Role Management** and run List, Role Detail, Create, Update, or Delete. Every request sends the exact configured `Origin` and the current `csrfToken`; role endpoints require the matching `role.read/create/update/delete` grant. The seeder grants the system catalog permissions to the `SUPER_ADMIN` role. Create returns `data: null`; copy the new role's `id` from List Roles into `roleId` before Detail, Update, or Delete.
5. Open **Admin Account Directory** to list or view an admin profile. These endpoints require Login, `admin-account.read`, the exact dashboard `Origin`, and the current `csrfToken`; set `adminId` to an account ID for Detail.
6. Open **Public Game Catalog** to call List or Popular without authentication. These public endpoints do not send cookies or CSRF tokens.
7. In **Auth**, run **Refresh Token** to rotate session cookies and CSRF token, or **Logout** to revoke the session family and clear cookies.

Each functional folder also includes an **OPTIONS preflight** request. Use those requests to inspect the configured CORS methods and headers; they do not mutate application data. Requests link to saved success responses and representative error responses so they appear as examples in Postman.

## Role management

- `GET {{baseUrl}}/api/admin/roles/v1/list` — optional `search`, `status`, `sortBy`, `sortOrder`, `page`, and `limit` (default page 1/limit 10, maximum 100).
- `GET {{baseUrl}}/api/admin/roles/v1/detail/{{roleId}}` — returns current role metadata and active permission keys.
- `POST {{baseUrl}}/api/admin/roles/v1/create` — creates a custom role and its permission grants atomically.
- `PUT {{baseUrl}}/api/admin/roles/v1/update/{{roleId}}` — updates supplied fields; a supplied `permissions` array replaces all grants.
- `DELETE {{baseUrl}}/api/admin/roles/v1/delete/{{roleId}}` — soft-deletes an unassigned custom role; request body is empty.

Mutations are throttled at 60 requests/admin/minute and 1,200 requests/IP/minute. Read endpoints use 600/admin/minute and 1,200/IP/minute. HTTP 429 responses include `Retry-After`.

## Game management

All game requests send the configured exact `Origin` and the `csrfToken` obtained by Login. The admin needs the endpoint permission (`game.read`, `game.create`, `game.update`, or `game.delete`); the seeder assigns these grants to the system Super Admin role. Only system Super Admin can send `isActive` or `isPopular` in create/update payloads.

- `GET {{baseUrl}}/api/admin/games/v1/list` — optional `q`, `isActive`, `isPopular`, `page`, and `limit` query parameters; page defaults to 1 and limit to 20 (maximum 100).
- `GET {{baseUrl}}/api/admin/games/v1/detail/{{gameId}}` — requires a canonical game ID.
- `POST {{baseUrl}}/api/admin/games/v1/create` — creates an inactive, non-popular game by default. The success response contains `data: null`, so run List and copy the created item's `id` into the local `gameId` environment variable before Detail, Update, or Delete.
- `PATCH {{baseUrl}}/api/admin/games/v1/update/{{gameId}}` — sends only fields to change; description/platform may be `null` to clear.
- `DELETE {{baseUrl}}/api/admin/games/v1/delete/{{gameId}}` — no request body; soft-deletes the game.

Each game request includes response assertions and a saved success example. `Cache-Control: no-store` is asserted for all responses. Create and Delete change local database state; use unique name/slug values and avoid running Delete against data you need.

Local environment sends `Origin: http://localhost:8080`, matching `.env`'s `CORS_ALLOWED_ORIGINS`. If dashboard is served from another origin, change `dashboardOrigin` to that exact browser origin and add it to `.env`'s `CORS_ALLOWED_ORIGINS`, then restart API.

## Admin account directory

- `GET {{baseUrl}}/api/admin/admin-users/v1/list` — optional `search`, `status`, `roleId`, `page`, and `limit`; defaults to page 1/limit 20 and maximum limit 100.
- `GET {{baseUrl}}/api/admin/admin-users/v1/detail/{{adminId}}` — returns a profile and its single active role. Credentials and session material are never included.

Both requests reuse the login cookies and `csrfToken`. They require the explicit `admin-account.read` permission and have 600 requests/admin/minute and 1,200 requests/IP/minute limits.

## Public game catalog

- `GET {{baseUrl}}/api/games/v1/list` — optional `search`, `platform`, `sort`, `page`, and `limit`; defaults to page 1/limit 20 and maximum limit 100.
- `GET {{baseUrl}}/api/games/v1/popular` — lists active, popular games; defaults to page 1/limit 6 and maximum limit 50.

These requests are unauthenticated and return only public display fields for active, non-deleted games. The service allows non-credentialed cross-origin GETs and limits each IP to 1,200 requests/minute.

Postman cookie jar should remain enabled for `localhost`; requests use `localhost` consistently. If calling from Postman Desktop Agent, Origin is still sent explicitly by the collection.

Endpoints:

- `POST {{baseUrl}}/api/admin/auth/v1/login` — JSON `{ "identifier": "...", "password": "..." }`
- `POST {{baseUrl}}/api/admin/auth/v1/refresh-token` — empty body, refresh cookie, `X-CSRF-Token`
- `POST {{baseUrl}}/api/admin/auth/v1/logout` — empty body, auth cookies, `X-CSRF-Token`

All endpoints return the API JSON envelope. Login and refresh return CSRF token in `data.csrfToken`; auth cookies are HttpOnly and managed by Postman.
