# Postman: Admin Authentication

## Import

Import these files in Postman:

- `admin-authentication.postman_collection.json`
- `local.postman_environment.json`

Select **Game Service Platform - Local**, then set `identifier` and `password` for a seeded admin account. Keep credentials in the local environment and do not commit them.

## Run

1. Start API with `go run ./cmd/api` or `air`.
2. Run **Login**. Postman stores auth cookies and collection test script saves `csrfToken`.
3. Run **Refresh Token** to rotate session cookies and CSRF token.
4. Run **Logout** to revoke the session family and clear cookies.

Local environment sends `Origin: http://localhost:8080`, matching `.env`'s `CORS_ALLOWED_ORIGINS`. If dashboard is served from another origin, change `dashboardOrigin` to that exact browser origin and add it to `.env`'s `CORS_ALLOWED_ORIGINS`, then restart API.

Postman cookie jar should remain enabled for `localhost`; requests use `localhost` consistently. If calling from Postman Desktop Agent, Origin is still sent explicitly by the collection.

Endpoints:

- `POST {{baseUrl}}/api/admin/auth/v1/login` — JSON `{ "identifier": "...", "password": "..." }`
- `POST {{baseUrl}}/api/admin/auth/v1/refresh-token` — empty body, refresh cookie, `X-CSRF-Token`
- `POST {{baseUrl}}/api/admin/auth/v1/logout` — empty body, auth cookies, `X-CSRF-Token`

All endpoints return the API JSON envelope. Login and refresh return CSRF token in `data.csrfToken`; auth cookies are HttpOnly and managed by Postman.
