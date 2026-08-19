# Student Union Main Server

Backend for Mae Fah Luang University Student Union services, in Go and PostgreSQL.

> 🚧 Under active development.

One binary serves three products that share a database and almost nothing else:

| Prefix | Product | Token | Users table |
|---|---|---|---|
| `/su-server/*` | The SU app — events, booths, steps, leaderboard | `JWT_SECRET` | `users` |
| `/wbw/*` | Walk-Bike-Week (เดินรอบดอย) — registration, check-ins, groups, chat, SOS | `JWT_SECRET`, WBW claim shape | `wbw_user` |
| `/clubfair/*` | Club Fair — booth stamps, prizes, announcements | `CLUBFAIR_JWT_SECRET` | `clubfair_users` |

They are separate on purpose. A user id means a different person in each, so a
token minted for one must never be accepted by another — see
[Three auth systems](#three-auth-systems).

## Tech Stack

- **Language:** Go 1.26
- **Database:** PostgreSQL 15, driven by `pgx/v5` — no ORM, SQL is written by hand
- **Router:** chi v5
- **Auth:** `golang-jwt/v5`, `bcrypt` for passwords
- **Push:** Firebase Cloud Messaging v1 over plain HTTP (no Firebase SDK)
- **Realtime:** Postgres `LISTEN`/`NOTIFY` behind an HTTP long-poll
- **Containers:** Docker Compose, Cloudflare Tunnel for public ingress
- **Architecture:** Handler → Service → Repository

## Project Structure

```
su-server/
├── cmd/
│   ├── main.go                 # Entry point: wiring + every route in the server
│   └── createadmin/main.go     # Bootstrap the first WBW admin
├── config/
│   └── database.go             # ConnectDB / ConnectPool / ConnectListener
├── db/migrations/              # golang-migrate, 000001 … 000022
├── docs/
│   └── chat-v2-deploy.md       # What long-poll needs from the proxy path
├── internal/
│   ├── handler/                # HTTP: decode, call one service, map error → status
│   ├── middleware/             # Three auth families + WriteJSON/WriteError
│   ├── model/                  # Request/response structs, JSON tags
│   ├── repository/             # SQL. The only layer that touches the database
│   └── service/                # Business rules, tokens, push, event fan-out
├── scripts/loadtest.js         # k6 load test
├── docker-compose.yml
├── Dockerfile
└── Makefile
```

`cmd/main.go` is the map of the system: every route, its middleware and the
reason it is public or not are written there, in one file, in route order.

### Layer rules

- **Handler** — decodes the body, pulls the caller from claims, calls exactly one
  service method, and translates sentinel errors into status codes with a
  `switch { case errors.Is(...) }`. It contains no SQL and no business rule.
- **Service** — the rules, and the only place a decision gets made. It returns
  sentinel errors (`service.ErrMissingCheckpoint`, `repository.ErrFull`, …), never
  an HTTP status.
- **Repository** — SQL and transactions. It maps SQLSTATE to sentinel errors
  (`IsPGCode(err, "23505")` → `ErrDuplicate`) so nothing above it imports `pgconn`.
- **Model** — plain structs. No behaviour.

### Two database handles

`config.ConnectDB` returns a single `*pgx.Conn`; `config.ConnectPool` returns a
`*pgxpool.Pool`. **New code takes the pool.** A single connection is not safe for
concurrent use — two requests on it can interleave on the wire. The older SU
repositories (event, user, step, leaderboard) still take `*pgx.Conn` and are
waiting to be migrated; everything WBW and Club Fair already uses the pool.

`config.ConnectListener` opens a third, dedicated connection for `LISTEN`.
It must never come from the pool: a listening connection blocks inside
`WaitForNotification` and would pin a pool slot forever.

## API Routes

Everything answers JSON. Errors are `{"error": "..."}` written by
`middleware.WriteError` — never Go's plain-text `http.Error`, which the
frontends cannot parse.

`GET /` returns `{"message": "SU Backend running"}` and is the health check.

Three routes sit outside every product prefix. `GET /privacy` and `GET /support`
are self-contained HTML pages, embedded in the binary rather than served from a
static directory, because the app stores require a reachable URL for both and a
missing one is a store rejection. `GET /clubfair/dashboard` is the same idea for
the staff console — see [Club Fair](#club-fair--clubfair).

### SU app — `/su-server`

#### Auth
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/su-server/auth/google` | — | Redirect to Google OAuth2 |
| `GET` | `/su-server/auth/google/callback` | — | OAuth2 callback |
| `POST` | `/su-server/auth/google/verify` | — | Verify a Google ID token (Flutter/mobile) |

#### Events
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/su-server/events` | — | List events |
| `GET` | `/su-server/events/{id}` | — | One event |
| `POST` | `/su-server/events` | staff | Create |
| `PUT` | `/su-server/events/{id}` | staff | Update |
| `DELETE` | `/su-server/events/{id}` | staff | Delete |

#### Booths
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/su-server/booths` | SU token | All booths. `event_id` is nullable. `secret` — the booth's check-in HMAC key — is never returned. Carries `zone`, `booth_code`, `name_en`, `about` and `icon` from the floor plan (migrations 000019–000020); `about` is null until the Student Union writes the copy |

#### Users
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/su-server/users/{id}` | self or staff | A record that belongs to one person |
| `PATCH` | `/su-server/users/{id}` | self or staff | Update profile |
| `GET` | `/su-server/users/email/{email}` | staff | Ownership cannot be expressed against an email, so knowing an address must not hand over the profile behind it |
| `POST` | `/su-server/users/insert` | staff | Create |
| `POST` | `/su-server/users/upsert` | staff | Create or update |

There is no `DELETE /users/{id}`. The route that used to sit there pointed at
`eventHandler.DeleteOneEvents` and deleted *events*; it was removed rather than
re-pointed, because nothing defines what becomes of a deleted student's
check-ins and step history. Auth on this subrouter is attached per-route rather
than with `r.Use`, so that the now-unregistered `DELETE` falls through to chi's
native 405 instead of being intercepted into a 401 that would make a removed
route look like a guarded one.

#### Steps
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/su-server/steps/{userID}` | self or staff | Day-by-day history |
| `GET` | `/su-server/steps/{userID}/range` | self or staff | `?from=&to=` |
| `POST` | `/su-server/steps/sync` | SU token | One day |
| `POST` | `/su-server/steps/sync/bulk` | SU token | Many days |

A step history is a record of where one named person was, and the leaderboard
hands out id-to-name for free — so reads are self-or-staff, not merely
signed-in. ⚠ The two sync routes still trust a body-supplied `user_id`; deriving
it from the claims means changing `SyncSteps`/`SyncManySteps` and is a separate
piece of work.

#### Leaderboard
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/su-server/leaderboard` | — | Full ranked list |
| `GET` | `/su-server/leaderboard/{userID}` | — | One user's rank |
| `POST` | `/su-server/leaderboard/update` | staff | Set a step count |
| `POST` | `/su-server/leaderboard/reset` | staff | Reset |

The two reads are public deliberately: a leaderboard is the campaign's front
page and far likelier to have a live caller than anything below it.

### Walk-Bike-Week — `/wbw`

`web-next` proxies `/api/*` here: `next.config.ts` strips `/api` and forwards to
`${API_UPSTREAM}/:path*` with `API_UPSTREAM=http://localhost:8080/wbw`.

Roles are `participant`, `staff`, `admin`. `requireAuth` must come before any
role check — `RequireRole` reads claims that `RequireAuth` put in the context.

#### Auth and capacity
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `POST` | `/wbw/auth/register` | — | Participant sign-up. `username` = `student_id`, role forced to `participant` |
| `POST` | `/wbw/auth/login` | — | Username + password |
| `POST` | `/wbw/auth/staff-register` | — | Staff sign up themselves; account is `pending` until an admin approves |
| `GET` | `/wbw/capacity` | — | Seats left. Reads one row |

The three `/auth` routes sit behind `middleware.ThrottleBacklog`: bcrypt at cost
10 burns ~80 ms of CPU per call, and thousands of students register in the same
few minutes. Excess requests **queue** and are answered late, and only get a 429
once the backlog times out — a delay, not a refusal. Tune with
`AUTH_THROTTLE_LIMIT`, `AUTH_THROTTLE_BACKLOG`, `AUTH_THROTTLE_TIMEOUT_SEC`.

`/wbw/capacity` is deliberately outside that group: the registration page calls
it before the form is shown, and it reads a single row, so making it wait behind
the bcrypt queue would be pure harm.

#### Me
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/wbw/me` | any | Own profile |
| `PATCH` | `/wbw/me` | any | Own photo only — every other field belongs to an admin |
| `GET` | `/wbw/me/progress` | any | Check-in progress for the Home screen tree, plus the emergency phone number |
| `POST` | `/wbw/me/feedback` | any | Rate a base |
| `POST` | `/wbw/me/sos` | any | Raise an emergency |
| `GET` | `/wbw/me/sos/active` | any | The caller's open case, if any |
| `GET` | `/wbw/me/sos/{id}` | any | One case |
| `POST` | `/wbw/me/sos/{id}/cancel` | any | Stand down |

`/me/progress` is polled every 60 s, which is why the central emergency number
rides along on it: the app has it cached *before* anything happens, not after.

#### Groups and chat
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/wbw/groups` | any | All groups |
| `GET` | `/wbw/groups/members/index` | any | Members of every group in one call |
| `POST` | `/wbw/groups/leave` | any | Leave. Costs one `leave_quota` |
| `GET` | `/wbw/groups/{groupId}/members` | any | Members |
| `POST` | `/wbw/groups/{groupId}/join` | any | Join. 409 while still in a group, or when quota is spent |
| `GET` | `/wbw/groups/{groupId}/messages` | any | Plain poll |
| `POST` | `/wbw/groups/{groupId}/messages` | any | Send |
| `GET` | `/wbw/groups/{groupId}/chat/sync` | any | **Long-poll**, `?wait=` up to 25 s |
| `POST` | `/wbw/groups/{groupId}/chat/read` | any | Move the read cursor; doubles as a "screen is open" heartbeat, which suppresses push for that member |

#### Devices, staff, notifications
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `POST` | `/wbw/devices/register` | any | FCM token, at login |
| `POST` | `/wbw/devices/unregister` | any | At logout. An empty body succeeds |
| `GET` | `/wbw/staff/checkpoints` | staff | Bases this staffer mans (admins see all) |
| `POST` | `/wbw/staff/checkin` | staff | Check a participant in from QR or BIB |
| `GET` | `/wbw/staff/sos` | staff | The SOS feed |
| `POST` | `/wbw/staff/sos/{id}/ack` | staff | Acknowledge |
| `POST` | `/wbw/staff/sos/{id}/resolve` | staff | Close |
| `GET` | `/wbw/notifications/public` | — | `audience=all` announcements, readable without logging in |
| `GET` | `/wbw/notifications` | any | Everything addressed to the caller |
| `POST` | `/wbw/notifications/{id}/read` | any | Mark read |
| `POST` | `/wbw/notifications` | staff | Publish |
| `GET` | `/wbw/notifications/sent` | staff | What this staffer sent |
| `GET`·`PUT`·`DELETE` | `/wbw/notifications/draft` | staff | One draft per staffer |
| `GET`·`POST` | `/wbw/notifications/presets` | staff | Canned messages |
| `DELETE` | `/wbw/notifications/presets/{id}` | staff | Remove one |

`/notifications/public` is declared *before* `r.Use(requireAuth)` in that
subrouter, because chi applies middleware only to routes declared after it.
Moving that line down silently closes a public page.

#### Admin
| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `GET` | `/wbw/admin/schools` | — | Public: the registration form needs it before anyone has a token |
| `GET` | `/wbw/admin/dashboard` | admin | Totals |
| `GET` | `/wbw/admin/logs` | admin | Audit log |
| `GET` | `/wbw/admin/bases-overview` | admin | Per-checkpoint status |
| `GET` | `/wbw/admin/feedback` | admin | Submitted base feedback |
| `GET` | `/wbw/admin/participants` | admin | List |
| `GET` | `/wbw/admin/participants/{id}/detail` | admin | Profile + last 10 group joins/leaves |
| `PATCH`·`DELETE` | `/wbw/admin/participants/{id}` | admin | Edit / remove (removal returns the seat) |
| `POST` | `/wbw/admin/participants/{id}/reset-password` | admin | New password |
| `GET`·`POST` | `/wbw/admin/checkpoints` | admin | List / create |
| `PATCH`·`DELETE` | `/wbw/admin/checkpoints/{id}` | admin | Edit / remove |
| `POST` | `/wbw/admin/checkpoints/{id}/staff` | admin | Assign a staffer |
| `DELETE` | `/wbw/admin/checkpoints/{id}/staff/{userId}` | admin | Unassign |
| `GET`·`POST` | `/wbw/admin/users` | admin | List / create any role |
| `PATCH`·`DELETE` | `/wbw/admin/users/{id}` | admin | Edit / remove |
| `POST` | `/wbw/admin/users/{id}/password` | admin | Set a password |
| `GET` | `/wbw/admin/staff-requests` | admin | Pending staff sign-ups |
| `POST` | `/wbw/admin/staff-requests/{id}/approve` · `/reject` | admin | Decide |

#### The 2,000-seat cap

Enforced in the database, not in Go. `wbw_capacity` (migration 000021) is a
one-row table — `id BOOLEAN PRIMARY KEY CHECK (id)` — carrying
`max_participants`, `taken`, and `CHECK (taken <= max_participants)`. A trigger
on `wbw_user` keeps `taken` in step with reality for every insert, delete and
role change.

`SELECT count(*)` then `INSERT` would not work: under READ COMMITTED, dozens of
concurrent registrations all read 1,999 and all insert. Funnelling every
registration through the same row lock is what makes the ceiling real. Deleting
a participant returns their seat. `wbw_capacity_test.go` proves it by racing
real registrations against a real Postgres.

The insert surfaces as `repository.ErrFull`, matched by SQLSTATE **and**
constraint name (`taken_within_max`) — 23514 alone would also match unrelated
CHECKs on other tables.

#### Chat v2 — long-poll over LISTEN/NOTIFY

`GET /wbw/groups/{id}/chat/sync?wait=25` holds the request open for up to 25
seconds (`maxWaitSeconds` in `wbw_chat_service.go`). `ChatEvents` owns a
dedicated `LISTEN` connection and redials it itself when the link drops.

Consequences anything in the request path has to respect — measured, not
assumed; see [docs/chat-v2-deploy.md](docs/chat-v2-deploy.md):

- **Never set `WriteTimeout` on the HTTP server.** `cmd/main.go` sets
  `ReadHeaderTimeout`, `ReadTimeout` and `IdleTimeout` and deliberately leaves
  `WriteTimeout` unset; anything below 25 s cuts every quiet long-poll in half.
- Cloudflare gives up on an origin at 100 s (error 524). 25 s is comfortably under.
- The path is `Cloudflare → cloudflared → backend:8080`. There is no nginx to
  configure `proxy_buffering` on.

SOS uses the same machinery on its own channel (`SOSEvents`).

#### Push

FCM v1 over plain HTTP, credentials from `GOOGLE_APPLICATION_CREDENTIALS` (or
`FIREBASE_SERVICE_ACCOUNT`, the name the old Node service used). **Unset means
push is silently off** and everything else — chat, notifications, SOS in-app —
still works fully.

### Club Fair — `/clubfair`

Registered only when `CLUBFAIR_JWT_SECRET` is set — see
[Club Fair tokens](#club-fair-tokens).

| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| `POST` | `/clubfair/auth/google` | — | Verify a Google ID token, upsert `clubfair_users`, return a session |
| `POST` | `/clubfair/auth/login` | — | Phone + password |
| `POST` | `/clubfair/auth/register` | — | Password sign-up. MFU email required; `student_id` derives from it |
| `GET` | `/clubfair/booths` | — | The 28 clubs in floor-plan order, with zone, booth code, English name and icon. `secret` is never returned |
| `GET` | `/clubfair/zones` | — | The three areas, Thai and English, in signage order |
| `GET` | `/clubfair/info` | — | When and where the fair is. The row the app and the website used to each hold a copy of |
| `GET` | `/clubfair/program` | — | The **published** running order. Drafts are invisible here |
| `GET` | `/clubfair/prizes` | — | The tiers a student can still reach. No `reached`/`claimed` — those need a token |
| `GET` | `/clubfair/me` | student | Who am I. Never returns `password_hash` or `oauth_subject` |
| `PATCH` | `/clubfair/me` | student | Phone, school, major. Absent fields are untouched |
| `PUT` | `/clubfair/me/password` | student | How a Google-only account gains the password fallback |
| `DELETE` | `/clubfair/me` | student | Delete your own account. `204`, and the stamps go with it |
| `GET` | `/clubfair/me/booths` | student | The booths you run. No role gate — an account with no assignments gets `[]`, which is the true answer for almost everyone |
| `GET` | `/clubfair/progress` | student | Count, visited booth ids, rank and prize tiers in one call |
| `GET` | `/clubfair/checkins` | student | This student's stamps, oldest first |
| `POST` | `/clubfair/checkins` | student | Record a scan. Idempotent on `client_id` |
| `GET` | `/clubfair/announcements` | student | The channel, with `mine` resolved per caller |
| `POST` | `/clubfair/announcements/{id}/reactions` | student | Toggle one of five emoji |
| `POST` | `/clubfair/announcements` | **staff** | Post |
| `DELETE` | `/clubfair/announcements/{id}` | **staff** | Soft delete |
| `GET` | `/clubfair/booths/{id}/checkin-code` | **staff · booth owner** | The booth display polls this for its current rotating code. A booth owner reaches only its own booths — the per-row half of the check is in the service, not the middleware |
| `POST` | `/clubfair/prizes/claim` | **staff** | Hand a prize over. Threshold re-checked server-side |
| `PUT` | `/clubfair/admin/info` | **staff** | Move the fair's dates, venue and notice |
| `GET` | `/clubfair/admin/program` | **staff** | The running order, drafts included |
| `POST`/`PUT`/`DELETE` | `/clubfair/admin/program[/{id}]` | **staff** | Edit the running order. `PUT` is a whole-row replace |
| `GET` | `/clubfair/admin/booth-categories` | **staff** | The five values `booth.category` allows |
| `POST`/`PUT`/`DELETE` | `/clubfair/admin/booths[/{id}]` | **staff** | Edit booths. `secret` is never accepted or returned; a delete is refused once anyone has scanned it |
| `GET` | `/clubfair/admin/prizes` | **staff** | Tiers with claim counts, retired ones included |
| `POST`/`PUT`/`DELETE` | `/clubfair/admin/prizes[/{id}]` | **staff** | Edit tiers. A delete is refused once anyone has claimed one — retire it instead |
| `GET` | `/clubfair/admin/participants` | **staff** | The roster, paged, searchable, with each student's stamp count. `limit` is capped at 200 |
| `GET` | `/clubfair/admin/participants/{id}` | **staff** | One student: profile, stamps, prizes, booths |
| `POST` | `/clubfair/admin/participants` | **admin** | Create an account. Admin-only for the same reason promoting one is |
| `PATCH` | `/clubfair/admin/participants/{id}` | **staff** | Flag an account. **Role changes are admin-only** |
| `PUT` | `/clubfair/admin/participants/{id}/booths` | **staff** | Reassign booths. Staff-level on purpose — granting the *role* is the admin decision, moving a shift is not |
| `PUT` | `/clubfair/admin/participants/{id}/password` | **admin** | Reset someone's password. Refused on your own account, and it does not end a live session |
| `GET` | `/clubfair/dashboard` | — | The admin console page (HTML). An empty shell — the numbers come from the endpoint below |
| `GET` | `/clubfair/admin/dashboard` | **admin** | The fair at a glance: students, total check-ins, prizes claimed, full sweeps |

#### The staff dashboard

`/clubfair/admin/*` is what the Club Fair website's dashboard talks to. Three
rules live in `ClubFairAdminService` rather than in a middleware, because each is
about the *edit* rather than about the route:

- only an **admin** may change a role — staff can flag, not promote;
- **nobody may act on their own account** — flagging yourself locks you out of
  the page you are standing in;
- the **last admin** cannot be demoted or flagged, because an empty admin list
  has nobody who can refill it.

Two deletes are refused rather than cascaded, and both refusals matter more than
they look. Deleting a **booth** cascades away every stamp collected at it, which
can push students back under a prize threshold they had already reached; deleting
a **prize tier** is blocked by the FK once anyone holds one. In both cases the
answer is to retire, not to delete — the same dance migration 000022 had to do by
hand for the 20-booth draw.

**There is no way to mint the first staff account from the web**, by design —
every account is created as a `student` by signing in, and the role editor needs
an admin. Use `make cf-staff email=... role=admin` (`cmd/createclubfairstaff`),
which promotes an account that has already signed in once. It does not create
one: the email is the key both credential paths join on, so a row created with a
guessed address is a row the real student's Google sign-in would never find.

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

#### Prize tiers

Two tiers, `Prize 1` at 15 booths and `Prize 2` at 28, and the names are meant to
stay that plain. They live in `clubfair_prize_tier` **rows**, not in code, so
moving a threshold needs no app release. Migration 000022 moves the first tier by
`UPDATE` rather than re-seeding it — its id is what `clubfair_prize_claim.tier_id`
points at, and the FK is `ON DELETE RESTRICT`, so a claim already handed out must
keep pointing somewhere.

## Three auth systems

They share this binary and nothing else. Each has its own claim struct, its own
context key, and its own middleware:

| System | Claims | Context key | Middleware |
|---|---|---|---|
| SU | `JWTClaims{user_id, user_type}` | `su_claims` | `RequireSUAuth`, `RequireSelfOrStaff`, `RequireSUStaff` |
| WBW | `WBWClaims{role, username, sub}` | `wbw_claims` | `RequireAuth`, `RequireRole` |
| Club Fair | `ClubFairClaims{cf_uid, cf_role}` | `clubfair_claims` | `RequireClubFairAuth`, `RequireClubFairRole` |

The three context keys are distinct *types*, not just distinct strings — one
package, three claim shapes, and a key collision would hand one system's claims
to another system's handler.

`clubfair_users.id` and `users.id` are different people, so a Club Fair token
that verified as an SU token would admit Club Fair student 5 to SU user 5's
profile and step history. Three barriers keep them apart, in descending strength:

1. **A separate secret.** Cross-verification fails at the signature, not at a
   claims check.
2. **Distinct JSON claim names** — `cf_uid`, never `user_id`. JSON decoding
   silently drops fields it does not recognise, so a foreign token that somehow
   verified arrives with id `0`.
3. **A required `aud: clubfair`**, checked by the parser rather than by hand
   afterwards.

### Club Fair roles

Four, on `clubfair_users.role`, held to a `CHECK` rather than an enum so that
widening the set is a `DROP`/`ADD CONSTRAINT` instead of an `ALTER TYPE` that
cannot run inside a transaction:

| role | reaches |
|---|---|
| `student` | the app — scanning, progress, prizes |
| `booth_owner` | `GET /booths/{id}/checkin-code`, **for its assigned booths only** |
| `staff` | everything under `/clubfair/admin`, plus any booth's code |
| `admin` | all of that, plus changing roles |

**`booth_owner` is not a rank between student and staff.** `requireClubFairStaff`
names `staff` and `admin` explicitly and must never gain it: the role exists so
that a screen left on a booth's table for two days is not also holding the
announcements channel and two thousand students' records. The wider gate is a
second middleware, `requireClubFairBoothScreen`, used on exactly one route — and
the per-booth half of that check lives in `ClubFairCheckInService.CurrentCode`,
because "may this user see *this* booth" is a question about a row that no role
middleware can answer.

Assignment is `clubfair_booth_owner` (migration 000024) — many-to-many, because a
booth is staffed in shifts and volunteers cover more than one.

`GET /booths/{id}/checkin-code` answers with **two** instants and they mean
different things. `expires_at` is when the code stops being the current one —
when a display should ask for the next. `accepted_until` is when it stops
*verifying*, three minutes later by default. A booth screen needs the second to
know whether what it is showing still works, and cannot compute it: the gap is
`CLUBFAIR_CHECKIN_MAX_AGE_SECONDS`, tunable per deployment. Both `Verify` and
`CurrentCode` read `maxAgeWindows()` so the advertised boundary and the enforced
one cannot drift.

⚠ **A role cannot be revoked.** It rides in a 30-day token and there is no
revocation list, so a demotion and `is_flagged` alike only bite at the next
sign-in. The assignment row is the one half of the booth check that reads live
state, which is why `UpdateParticipant` **deletes** an owner's assignments when
their role moves off `booth_owner`. Follow it through the code and the reason
is plain: the role in that token still says `booth_owner`, so with the assignment
rows left in place `CurrentCode` would find the membership and answer — the
demotion having already returned `200`.

Barriers 2 and 3 are also why SU and WBW can share `JWT_SECRET` today:
`su_auth.go` rejects `UserID <= 0`, which works *only* because the two claim
shapes differ. Two systems sharing a secret **and** a shape cannot be told apart
at all. `clubfair_token_service_test.go` asserts the isolation in both directions.

With `CLUBFAIR_JWT_SECRET` unset the server still starts and logs loudly, but the
`/clubfair` routes are not registered — a 404 on a new surface, rather than a live
endpoint with no security, and Walk-Bike-Week does not go down over a Club Fair
variable. (`JWTService` does the opposite and calls `os.Exit(1)` on an empty
secret, because it gates routes that are already live.)

## Getting Started

### Prerequisites

- [Go 1.26+](https://golang.org/dl/)
- [Docker](https://www.docker.com/) & Docker Compose
- [golang-migrate](https://github.com/golang-migrate/migrate) CLI (for the `make migrate-*` targets)
- [air](https://github.com/air-verse/air) for `make watch`, [httpie](https://httpie.io/) for the API targets

### Setup

```bash
git clone https://github.com/Student-Union-MFU/su-server.git
cd su-server

cp .env.example .env      # then fill it in — .env is gitignored, never commit it

docker compose up -d database   # Postgres only
make migrate-up                 # apply db/migrations
make dev                        # go run cmd/main.go

make wbw-admin user=admin pass=<password> name="ชื่อที่แสดง"   # first WBW admin
```

`docker compose up` runs the `migrate` service on every boot, so the schema stays
in sync whether you migrate through compose or through the Makefile.

### Running it in Docker

```bash
make up        # build and start database, migrate, backend, cloudflared
make ps        # what is running
make logs      # follow the backend
make restart   # rebuild and restart the backend alone
make down      # stop everything
make psql      # a psql shell on the database
```

Four services: `database` (Postgres 15), `migrate` (one-shot, applies
`db/migrations` then exits — safe on every boot, prints "no change"), `backend`,
and `cloudflared`. Both published ports are bound to `127.0.0.1`, so the stack is
reachable from this box and through the tunnel, and from nowhere else. A bare
`8080:8080` would publish it unencrypted on every interface.

Ingress is a **named** Cloudflare tunnel at `api.studentunion.social`: put the
connector token in `TUNNEL_TOKEN`, then map the hostname to `http://backend:8080`
(not `localhost:8080` — inside the container that resolves to cloudflared itself
and 502s) in Zero Trust → Networks → Tunnels → Public Hostname. Because that
hostname is stable, it is what backs the Google OAuth redirect URI. Cloudflare
terminates TLS at its edge and reaches us over an outbound connection, which is
the whole point: the server sits on a private address behind campus NAT where
Let's Encrypt could never reach it.

⚠ The `database` service hardcodes `POSTGRES_USER=admin` / `POSTGRES_PASSWORD=yion`
/ `POSTGRES_DB=sudb` while `migrate` and `backend` read `DB_USER`/`DB_PASS`/`DB_NAME`
from `.env`. They must agree, or `migrate` cannot log in on a fresh volume.

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `ENV` | `production` makes the OAuth state cookie `Secure`. Keep `development` for plain-HTTP local work or login breaks | `development` |
| `SERVER_PORT` | Port. Falls back to `PORT`, then `8080` | `8080` |
| `SERVER_HOST` | Host the Makefile API targets aim at. Not read by Go | `localhost` |
| `BASE_URL_DEVELOPMENT` | **Unused.** Nothing in Go reads it, and the Makefile stopped trusting it because `.env.example` has it as a bare origin while `.env` files in circulation have `/su-server` on the end | |
| `DB_HOST` | Postgres host. Compose overrides this to `database` | `localhost` |
| `DB_PORT` · `DB_USER` · `DB_PASS` · `DB_NAME` | Postgres connection | `5432` `admin` · · `sudb` |
| `DB_MAX_CONNS` | Pool ceiling. (backend replicas × this) must stay under Postgres `max_connections` | `20` |
| `DB_MIN_CONNS` | Warm connections, to blunt first-burst latency | `2` |
| `JWT_SECRET` | SU + WBW signing key | |
| `JWT_EXPIRY` | Token lifetime, as a Go duration | `24h` |
| `GOOGLE_CLIENT_ID` · `GOOGLE_CLIENT_SECRET` | Google OAuth2 | |
| `GOOGLE_REDIRECT_URL` | Must match Google Cloud Console exactly | `https://api.studentunion.social/su-server/auth/google/callback` |
| `GOOGLE_ALLOWED_AUDIENCES` | Extra accepted OAuth client ids, comma-separated, for the Android and iOS clients | |
| `CORS_ALLOWED_ORIGINS` | Extra browser origins, comma-separated. **Added to** the built-in `localhost:3000`, `localhost:3001`, `*.trycloudflare.com` | |
| `GOOGLE_APPLICATION_CREDENTIALS` | Firebase service-account path **as the container sees it**. Unset = push silently off | `/run/secrets/firebase-adminsdk.json` |
| `FIREBASE_SERVICE_ACCOUNT` | Fallback name for the same thing, from the old Node service | |
| `TUNNEL_TOKEN` | Cloudflare connector token. A secret — it grants the right to route traffic to you | |
| `AUTH_THROTTLE_LIMIT` | Concurrent bcrypt calls | `40` |
| `AUTH_THROTTLE_BACKLOG` | How many wait in the queue | `2000` |
| `AUTH_THROTTLE_TIMEOUT_SEC` | How long a queued request waits before 429 | `25` |
| `WBW_EMERGENCY_PHONE` | Central number, returned with `/wbw/me/progress`. Empty = the app uses its built-in default | |
| `CLUBFAIR_JWT_SECRET` | **Separate** Club Fair key. Unset = `/clubfair` not registered | |
| `CLUBFAIR_CHECKIN_MAX_AGE_SECONDS` | How old a booth code may be — also the shared-screenshot window | `180` |
| `CLUBFAIR_INTAKE_PREFIXES` | Which intakes may **open a new account**, as leading student-id digits — `69` is the 2569 entry, so the default is the four years on campus. `*` accepts any. Existing accounts sign in regardless, so nobody is locked out retrospectively | `66,67,68,69` |
| `WBW_DB_TESTS` · `WBW_TEST_DSN` | Turn the real-Postgres tests on; see [Tests](#tests) | `1` |

### Migrations

`golang-migrate`, `db/migrations`, `NNNNNN_name.{up,down}.sql`, always in a pair.

```bash
make migrate-up               # apply everything pending
make migrate-down             # roll back 1     (make migrate-down N=3 for more)
make migrate-new name=thing   # create the .up.sql / .down.sql pair, next in sequence
make migrate-version          # what the DB thinks it is on
make migrate-force V=8        # clear a dirty flag by stamping a version
make migrate-drop             # drops EVERY table. prompts first
make tables                   # \dt
```

A `(dirty)` suffix means a migration aborted partway; golang-migrate then refuses
every later run *including the fix*, and because the backend waits on the
`migrate` service the whole stack stops coming up. Check what the schema actually
contains, `force` back to the last version that really applied, then re-run.

`-path` takes the migrations **directory**, never a single file.

### Makefile — API targets

`make` with no target prints every target with a one-line description. The API
targets are httpie one-liners for poking a running server by hand — not tests.
Each appends its own prefix to `API`, which defaults to
`http://$(SERVER_HOST):$(SERVER_PORT)`:

```bash
make get-events                                   # public
make wbw-capacity                                 # public
make cf-zones                                     # public

make wbw-login user=6531503001 pass=secret        # prints a token
make get-user id=1 TOKEN=eyJhbGci...              # everything else needs one
make get-events API=https://api.studentunion.social   # or hit the tunnel
```

Targets that take an argument fail with a usage line when it is missing, rather
than firing a request at a URL with an empty path segment and returning a
puzzling 404.

### Tests

```bash
make test        # unit tests, no database needed
make test-db     # + the tests that need a real Postgres (WBW_DB_TESTS=1)
make check       # fmt + vet + test
```

Tests that prove database behaviour — the capacity cap, group-leave quota, SOS
fan-out, feedback — run against real Postgres and skip unless `WBW_DB_TESTS=1`.
That is deliberate: row locks, transactions and CHECK constraints are exactly
what a fake pool does not have, so mocking them out would mean not testing the
thing. Connection comes from `.env`, or `WBW_TEST_DSN` to override.

⚠ Those tests write to the database they are pointed at. Point them at dev.
Fixtures use prefixes that cannot collide with real data (`captest-`, never a
digit prefix that looks like a student id — cleaning up with `LIKE '693%'` once
deleted a real dev account).

## Conventions

Working on this codebase — human or agent — [CLAUDE.md](./CLAUDE.md) is the
short version: comment language, error handling, where a decision belongs, and
what not to touch.

## License

See [LICENSE](./LICENSE).
