# CLAUDE.md

Guidance for Claude Code (and any agent) working in this repository. Read
[README.md](./README.md) for what the system *is*; this file is how to write in it.

## Orientation

One Go binary, three products sharing a database and almost nothing else:
`/su-server/*` (SU app), `/wbw/*` (Walk-Bike-Week, เดินรอบดอย), `/clubfair/*`
(Club Fair). Layering is Handler → Service → Repository, wired by hand in
`cmd/main.go`.

**Start at `cmd/main.go`.** Every route, its middleware, and the reason it is
public or guarded is written there in route order. It is the map; nothing is
auto-registered anywhere.

## Build, run, test

```bash
make dev                         # go run cmd/main.go
make watch                       # air, hot reload
go build ./... && go vet ./...   # before claiming anything compiles
go test ./...                    # unit tests, no database
WBW_DB_TESTS=1 go test ./...     # + real-Postgres tests
make migrate-up                  # apply db/migrations
```

Do not run `make migrate-drop`, `make migrate-force`, or anything that writes to
a database, without being asked. `WBW_DB_TESTS=1` writes to whatever `.env`
points at — confirm that is a dev database before running it.

## The rules that are easy to break

### Comment language follows the feature, not your preference

- **WBW code and migrations: Thai.** User-facing WBW error strings are Thai too
  (`"ต้องล็อกอินก่อน"`), because the app prints `body.error` verbatim.
- **Club Fair and newer SU code: English.**
- Match the file you are editing. Never translate an existing comment as a side
  effect of a change.

Comments here explain **why**, at length, and often carry a warning or a measured
number (`docs/chat-v2-deploy.md` records what was actually measured, not what was
expected). Prose comments above a tricky function are the house style — a `/* */`
block explaining the hazard is normal and welcome. Do not strip them, do not
compress them into one line, and do not add restating-the-code comments
(`// increment i`) to compensate.

### Layer boundaries

| Layer | Does | Never does |
|---|---|---|
| `handler` | Decode body, read claims, call **one** service method, map sentinel errors to status codes | SQL, business rules, `pgconn` imports |
| `service` | The rules and the decisions. Returns sentinel errors | HTTP status codes, `http.ResponseWriter` |
| `repository` | SQL, transactions, SQLSTATE → sentinel error | Deciding policy |
| `model` | Structs with JSON tags | Behaviour |

Handlers map errors with a `switch { case errors.Is(err, X): ... }` and end in
`default:` — see `internal/handler/wbw_staff_handler.go`. A new failure mode
means a new exported sentinel (`var ErrThing = errors.New("...")`), not a string
comparison and not an inline `http.Error`.

### Responses

Always `middleware.WriteJSON(w, status, v)` and `middleware.WriteError(w, status,
msg)`. Never `http.Error` — it sends plain text, which the frontends cannot
parse. Errors are always `{"error": "..."}`.

### Database

- **Every repository takes `*pgxpool.Pool`.** A single `*pgx.Conn` is not
  concurrency safe, and pgx neither locks nor errors — concurrent callers
  interleave on the wire. `config.ConnectDB` is gone; the last four holdouts
  (event, user, step, leaderboard) moved onto the pool. `LISTEN` is the only
  bare connection left, for the opposite reason.
- **Never borrow a pool connection for `LISTEN`.** Use `config.ConnectListener`.
  A listening connection blocks in `WaitForNotification` and would pin a slot
  forever.
- Parameterised SQL, always. No string-built queries, no ORM.
- Multi-table writes go in one transaction with `defer tx.Rollback(ctx)` — see
  `WBWAuthRepository.Register`.
- Map SQLSTATE with `IsPGCode(err, "23505")`. For CHECK violations (23514) match
  the **constraint name** too — the code alone is shared by unrelated
  constraints on other tables.

### Invariants that belong in the database

Counting-then-inserting loses races under READ COMMITTED. Where a ceiling has to
hold — `wbw_capacity` (2,000 seats), `participant_group` capacity — it is a
one-row table plus a `CHECK` plus a trigger, so every writer queues on the same
row lock. Adding a new limit? Follow that pattern; do not add a `SELECT count(*)`
guard in Go.

### Do not set `WriteTimeout` on the HTTP server

`cmd/main.go` sets `ReadHeaderTimeout`, `ReadTimeout` and `IdleTimeout` and
leaves `WriteTimeout` unset on purpose. Chat, notifications and SOS long-poll for
up to 25 s (`maxWaitSeconds`); any `WriteTimeout` under that cuts every quiet
poll. There is a comment saying so — it is not an oversight to be tidied.

### Auth: three systems, kept apart deliberately

`RequireSUAuth`/`RequireSelfOrStaff`/`RequireSUStaff` (SU) ·
`RequireAuth`/`RequireRole` (WBW) · `RequireClubFairAuth`/`RequireClubFairRole`
(Club Fair). Distinct claim structs, distinct JSON field names, distinct context
key *types*.

- Role checks always come **after** the matching auth middleware — they read
  claims that it put in the context.
- Never reuse another system's token service, secret, claim name, or context key.
  Read the block comment in `internal/service/clubfair_token_service.go` before
  touching any of it.
- New route: decide auth *at the route*, in `cmd/main.go`, and say why in a
  comment if it is public.

### chi ordering traps

- Middleware applies only to routes declared **after** `r.Use`. `/wbw/notifications/public`
  is public purely because it is declared above the `requireAuth` group.
- Nest auth-only verbs in an `r.Group` rather than `r.Use`-ing a whole subrouter,
  so an unregistered verb gets chi's 405 instead of a misleading 401. `/su-server/users`
  attaches auth per-route for exactly this reason and explains it in place.

### Migrations

- `NNNNNN_name.up.sql` **and** `.down.sql`, always both, next number in sequence.
- Header comment explaining what and why, in the feature's language.
- Never edit an applied migration; add a new one.
- Data that policy will change (prize thresholds, seat caps) lives in **rows**, so
  moving it needs no app release. When you move such a row, `UPDATE` it rather
  than delete-and-reseed if anything holds a FK to its id — see migration 000022.

## Testing

- Plain `testing` with `t.Fatalf`/`t.Errorf`. No assertion framework — `testify`
  is an indirect dependency and is never imported. One `Test…` function per
  behaviour; `t.Run` only where a case table genuinely helps.
- Test names and failure messages follow the file's language — Thai in WBW tests.
- Behaviour that *is* database behaviour (locks, transactions, constraints) gets
  a real-Postgres test gated on `WBW_DB_TESTS=1`, not a fake pool. A fake has no
  transaction, no lock, no constraint, so mocking is equivalent to not testing.
- Fixtures must use identifiers that cannot collide with real data — prefixes
  with a hyphen (`captest-`), never digits that look like a student id.
- Always clean up written rows, on the failure path too.

## Commits

Format is `type(scope): summary`, scope being the feature area
(`feat(wbw):`, `fix(group):`, `docs(chat):`, `chore:`). Summaries are usually
Thai for WBW work and English for SU/Club Fair. Say what changed and why it
mattered — `fix(sos): แถวแจ้งเตือนของกลุ่มไม่เคยเกิดขึ้นจริงเลยสักแถว` is the register.

Do not commit or push unless asked. Never commit `.env` or anything under
`secrets/`.

## When something looks wrong

Several comments in this repo document a deliberate decision that looks like a
bug (no `WriteTimeout`; `/wbw/capacity` outside the throttle group; per-route
auth on `/su-server/users`; the missing `DELETE /users/{id}`). Read the comment
before "fixing" it. If it still seems wrong, say so — don't silently change it.

Known-open items, already documented, not to be quietly fixed as drive-by work:

- `POST /su-server/steps/sync` and `/sync/bulk` trust a body-supplied `user_id`.
- `EventRepository.GetAllEvents` fetches images with one query per event. It
  closes the outer rows first, so it is correct — but it is an N+1 all the same.
- `docker-compose.yml` hardcodes `POSTGRES_USER/PASSWORD/DB` on the `database`
  service while `migrate` and `backend` read them from `.env`. They have to agree
  or a fresh volume cannot be migrated.
- `BASE_URL_DEVELOPMENT` is dead config: no Go code reads it, and the two `.env`
  variants disagree on whether it carries the `/su-server` prefix. The Makefile
  builds its base URL from `SERVER_HOST`/`SERVER_PORT` instead.
