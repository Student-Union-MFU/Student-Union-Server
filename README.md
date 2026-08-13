# Student Union Main Server
Backend server for Mae Fah Luang University Student Union services, built with Go and PostgreSQL.

> 🚧 This project is currently under active development.

## Tech Stack

- **Language:** Go
- **Database:** PostgreSQL 15
- **Containerization:** Docker & Docker Compose
- **Router:** Chi
- **Architecture:** Handler → Service → Repository pattern

## Project Structure

```
su-server/
├── cmd/
│   └── main.go                     # Entry point
├── config/
│   └── database.go                 # Database connection & config
├── db/
│   └── migrations/
│       ├── 000001_init.up.sql      # Events & lost and found schema
│       ├── 000001_init.down.sql
│       ├── 000002_user.up.sql      # Users schema
│       ├── 000002_user.down.sql
│       ├── 000003_steps.up.sql     # Steps schema
│       └── 000004_leaderboard.up.sql # Leaderboard schema
├── internal/
│   ├── handler/                    # HTTP handlers (request/response)
│   │   ├── event_handler.go
│   │   ├── leaderboard_handler.go
│   │   ├── oauth_handler.go
│   │   ├── step_handler.go
│   │   └── user_handler.go
│   ├── middleware/                 # HTTP middleware
│   ├── model/                      # Data models
│   │   ├── event_model.go
│   │   ├── event_image_model.go
│   │   ├── leaderboard_model.go
│   │   ├── step_model.go
│   │   └── user_model.go
│   ├── repository/                 # Database queries
│   │   ├── event_repository.go
│   │   ├── leaderboard_repository.go
│   │   ├── step_repository.go
│   │   └── user_repository.go
│   └── service/                    # Business logic
│       ├── event_service.go
│       ├── jwt_service.go
│       ├── leaderboard_service.go
│       ├── oauth_service.go
│       ├── step_service.go
│       └── user_service.go
├── docker-compose.yml
├── Dockerfile
└── Makefile
```

## API Routes

### Auth
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/auth/google` | Redirect to Google OAuth2 login |
| `GET` | `/auth/google/callback` | Google OAuth2 callback |
| `POST` | `/auth/google/verify` | Verify Google ID token (Flutter mobile) |

### Events
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/events` | List all events |
| `GET` | `/events/:id` | Get event by ID |
| `POST` | `/events` | Create a new event |
| `PUT` | `/events/:id` | Update an event |
| `DELETE` | `/events/:id` | Delete an event |

### Booths
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/booths` | List all booths. `event_id` is nullable — a booth with no event assigned yet returns `event_id: null`. `secret` (the booth's check-in HMAC key) is deliberately never returned on this endpoint. Also carries `zone`, `booth_code`, `name_en`, `about` and `icon` from the floor plan (migrations 000016–000017); `about` is null on every booth until the Student Union writes the copy. |

### Club Fair

Registered under `/clubfair` only when `CLUBFAIR_JWT_SECRET` is set — see
[Club Fair tokens](#club-fair-tokens).

| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `POST` | `/clubfair/auth/google` | — | Verify a Google ID token, upsert `clubfair_users`, return a session |
| `POST` | `/clubfair/auth/login` | — | Phone + password |
| `POST` | `/clubfair/auth/register` | — | Password sign-up. MFU email required; `student_id` derives from it |
| `GET` | `/clubfair/booths` | — | The 28 clubs in floor-plan order, with zone, booth code, English name and icon. `secret` is never returned |
| `GET` | `/clubfair/zones` | — | The three areas, Thai and English, in signage order |
| `GET` | `/clubfair/me` | student | Who am I. Never returns `password_hash` or `oauth_subject` |
| `PATCH` | `/clubfair/me` | student | Phone, school, major. Absent fields are untouched |
| `PUT` | `/clubfair/me/password` | student | How a Google-only account gains the password fallback |
| `GET` | `/clubfair/progress` | student | Count, visited booth ids, rank and prize tiers in one call |
| `GET` | `/clubfair/checkins` | student | This student's stamps, oldest first |
| `POST` | `/clubfair/checkins` | student | Record a scan. Idempotent on `client_id` |
| `GET` | `/clubfair/announcements` | student | The channel, with `mine` resolved per caller |
| `POST` | `/clubfair/announcements/{id}/reactions` | student | Toggle one of five emoji |
| `POST` | `/clubfair/announcements` | **staff** | Post |
| `DELETE` | `/clubfair/announcements/{id}` | **staff** | Soft delete |
| `GET` | `/clubfair/booths/{id}/checkin-code` | **staff** | The booth display polls this for its current rotating code |
| `POST` | `/clubfair/prizes/claim` | **staff** | Hand a prize over. Threshold re-checked server-side |
| `GET` | `/clubfair/dashboard` | — | The admin console page (HTML). An empty shell — the numbers come from the endpoint below |
| `GET` | `/clubfair/admin/dashboard` | **admin** | The fair at a glance: students, total check-ins, prizes claimed, full sweeps |

`POST /clubfair/checkins` takes `{payload, client_id, device_time}` and answers
with one of three outcomes, all 2xx — "you already collected this" is an answer,
not a failure:

| `outcome` | Status | Means |
|---|---|---|
| `recorded` | 201 | A new stamp |
| `duplicate_request` | 200 | Same `client_id` again — the app replaying its queue |
| `already_scanned` | 200 | A different scan of a booth already collected |

Failures: `400` a code that is not the fair's or does not verify, `409` a code
whose window has passed (re-scan), `404` no such booth.

#### Club Fair tokens

Club Fair signs with **its own secret**, `CLUBFAIR_JWT_SECRET`, and this is not
optional tidiness. `clubfair_users.id` and `users.id` are different people, so a
Club Fair token that verified as an SU token would admit Club Fair student 5 to SU
user 5's profile and step history. `su_auth.go` guards the WBW direction of this
by rejecting `UserID <= 0`, which works only because those two claim shapes
differ — two systems sharing a secret *and* a shape cannot be told apart at all.

Three barriers, in order of strength: a separate secret, so cross-verification
fails at the signature; distinct JSON claim names (`cf_uid`, not `user_id`); and a
required `aud: clubfair` checked by the parser. `clubfair_token_service_test.go`
asserts the isolation in both directions.

With the variable unset the server still starts and logs loudly, but the
`/clubfair` routes are not registered — a 404 on a new surface rather than a
working endpoint with no security, and Walk-Bike-Week does not go down over a
Club Fair variable.

#### Rotating check-in codes

Each booth's 32-byte `secret` (migration 000009) never leaves the server. A device
at the booth polls `/clubfair/booths/{id}/checkin-code` and displays a QR that
changes every 30 seconds:

```
clubfair://checkin?b=<booth id>&w=<window>&c=<code>

window = unix / 30
code   = HMAC-SHA256(secret, "<booth id>:<window>")[:12]
```

The window travels in the clear on purpose — it is only a timestamp, and stating
it means the server verifies one candidate exactly, so the accepted age is an
explicit policy rather than however far a search loop happens to run.

**This stops a student inventing a code** for a booth they never visited. **It
does not stop a screenshot shared inside the accepted age** — nothing a booth
displays can be protected from being photographed. That age is therefore the
sharing window: `CLUBFAIR_CHECKIN_MAX_AGE_SECONDS`, three minutes by default, and
the first number to turn down if cheating shows up.

⚠ It is in direct tension with offline sync. A student who scans in a dead spot
and uploads twenty minutes later has a code the server refuses, and the honest
answer is a `409` telling them to scan again rather than widening the window to
twenty minutes for everyone.

### Users
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/users/:id` | Get user by ID |
| `GET` | `/users/email/:email` | Get user by email |
| `POST` | `/users` | Create a new user |
| `PATCH` | `/users/:id` | Update user profile |

### Steps
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/steps/:userID` | Get all steps for a user |
| `GET` | `/steps/:userID/range` | Get steps by date range (`?from=&to=`) |
| `POST` | `/steps/sync` | Sync a single day's steps |
| `POST` | `/steps/sync/bulk` | Bulk sync multiple days |

### Leaderboard
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/leaderboard` | Get full ranked leaderboard |
| `GET` | `/leaderboard/:userID` | Get a user's current rank |
| `POST` | `/leaderboard/update` | Update a user's step count |
| `POST` | `/leaderboard/reset` | Reset the leaderboard |

## Getting Started

### Prerequisites

- [Go 1.21+](https://golang.org/dl/)
- [Docker](https://www.docker.com/) & Docker Compose
- [golang-migrate](https://github.com/golang-migrate/migrate)

### Setup

```bash
# Clone the repo
git clone https://github.com/yourname/su-server.git
cd su-server

# Copy env file and fill in values
cp .env.example .env

# Start PostgreSQL
docker-compose up -d

# Run migrations
make migrate-up

# Start dev server with hot reload
make dev
```

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `BASE_URL` | Base URL | `http://localhost:8080` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_USER` | Database user | `admin` |
| `DB_PASSWORD` | Database password | `secret` |
| `DB_NAME` | Database name | `sudb` |
| `GOOGLE_CLIENT_ID` | Google OAuth2 client ID | |
| `GOOGLE_CLIENT_SECRET` | Google OAuth2 client secret | |
| `GOOGLE_REDIRECT_URL` | Google OAuth2 redirect URL | `http://localhost:8080/auth/google/callback` |
| `JWT_SECRET` | JWT signing secret | |
| `JWT_EXPIRY_HOURS` | JWT expiry in hours | `24` |
| `CLUBFAIR_JWT_SECRET` | **Separate** Club Fair signing key. Unset = `/clubfair` routes not registered | |
| `CLUBFAIR_CHECKIN_MAX_AGE_SECONDS` | How old a booth code may be. This is also the shared-screenshot window | `180` |
| `GOOGLE_ALLOWED_AUDIENCES` | Extra accepted OAuth client ids, comma-separated, for the Android and iOS clients | |
| `CLUBFAIR_INTAKE_PREFIXES` | Which intakes may **open a new account**, as the leading digits of a student id — `69` is the 2569 entry. Comma-separated for two intakes, `*` to accept any. An account that already exists signs in regardless, so staff and earlier students are never locked out | `69` |

### Makefile Commands

```bash
make dev              # Start dev server with hot reload (air)
make migrate-up       # Run all pending migrations
make migrate-down     # Roll back last migration

# Events
make get-events
make get-event id=1
make create-event title="..." content="..." location="..." date="..." time="..." link="..."
make update-event id=1 title="..." content="..."
make delete-event id=1

# Users
make get-user id=1
make get-user-email email="640@lamduan.mfu.ac.th"
make create-user name="..." email="..." usertype="student" student_id="..." major="..." school="..." avatar_url="..." oauth_subject="..."
make update-user id=1 major="..." school="..." student_id="..."

# Steps
make sync-steps
make sync-steps-bulk
make get-steps userID=1
make get-steps-range userID=1 from=2026-06-16 to=2026-06-22

# Leaderboard
make get-leaderboard
make get-user-rank userID=1
make update-leaderboard userID=1 step_count=8432
make reset-leaderboard
```

## License

See [LICENSE](./LICENSE) for details.
