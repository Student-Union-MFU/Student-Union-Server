# Server stats dashboard — build notes

Handoff document. Read [CLAUDE.md](../CLAUDE.md) first for how to write in this
repo; this file is only about the dashboard and what it needs underneath it.

Written in English because this is server-wide infrastructure, not WBW. Follow
the usual rule when you edit code: the feature's language, not your preference.

## What this is for

The server slowed down at the Club Fair on **2026-08-22** (~3,000 students). It
did not crash and nothing errored — it just got slower, and there was no way to
say *why* from inside the process. The cause turned out to be one query
(`ClubFairFairRepository.rank`, since removed from the hot path) that scanned the
whole of `clubfair_checkin` on every `GET /clubfair/progress`.

It was found by reading code. That does not scale to the next one. The dashboard
exists so the next slowdown is **attributable in a minute instead of an
afternoon** — and so a claim like "this change bought 10× headroom" can be
checked rather than believed.

Design everything here around that goal. A number nobody would act on is a
number not worth collecting.

## What exists today

One endpoint, and it is the whole of the current telemetry:

| Route | Guard | Returns |
|---|---|---|
| `GET /su-server/admin/db-pool` | `RequireSUAuth` + `RequireSUStaff` | `pgxpool.Stat()` plus two derived numbers |

Handler: `internal/handler/db_pool_handler.go`. It holds the `*pgxpool.Pool`
directly with no service or repository behind it — deliberate, and the comment
block on `DBPoolHandler` explains why (there is no rule to apply, only a counter
to read). `LegalPrivacyPage` is service-less for the same reason. **Follow that
precedent for the other stats endpoints below; do not invent a `StatsService`
that only forwards calls.**

The two derived fields matter more than the raw counters:

- `empty_acquire_pct` — share of acquires that had to **wait** for a free
  connection. While this is ~0 under load, the pool is not the bottleneck and
  raising `DB_MAX_CONNS` cannot help. This is the number that settles pool
  sizing arguments.
- `mean_acquire_ms` — what the waiting costs on average.

Both are guarded against a divide-by-zero that would emit `NaN`;
`encoding/json` refuses to encode `NaN`, and `WriteJSON` has already sent the
200 by then, so the client would get a truncated body. See
`derivePoolRates` and its tests.

Everything else in this document still needs building.

> **Update, 2026-08-25 — there are now two dashboards, and this section
> describes only the first.**
>
> The embedded page below was built as specified and is still served at
> `GET /su-server/stats`. Alongside it there is now a **Next.js app** in the
> `su-server-stats-dashboard` repo, which is the richer everyday view; its
> README explains the split.
>
> Keeping both is deliberate rather than indecisive. The embedded page needs no
> build step and no Node, so it works when the Next app does not — which is
> exactly the situation a stats page exists for. It is the floor, not the
> superseded version.
>
> Two things below are therefore now half-true:
>
> - **"No build step, no npm"** applies to the embedded page. The Next app has
>   both, and buys with them an HttpOnly session cookie and a server-side proxy
>   — neither of which the embedded page can have, because it has no server of
>   its own.
> - **The auth pattern.** The Next app does not put a token in `localStorage`.
>   It proxies every call through `app/api/su/[...path]`, so the browser never
>   holds the credential and never makes a cross-origin request. su-server
>   gained a `?redirect=` allowlist (`oauthRedirectTargets` in
>   `oauth_handler.go`) so the Google callback can land in either dashboard
>   rather than on a page of JSON to copy out of.
>
> The argument against **Prometheus + Grafana** is untouched and still stands:
> the objection was two more containers on a memory-pressured box, not a build
> step.

## Architecture: embed it in the Go binary

**Do this**, following `internal/handler/clubfair_dashboard.html`:

- One `.html` file, `//go:embed`-ed into the handler package.
- Served by a handler that just sets `text/html; charset=utf-8` and writes it.
- No build step, no `npm`, no static directory for the Dockerfile to `COPY`.

The reasoning is on `DashboardPage` in `clubfair_admin_handler.go` and it holds
here too: the Dockerfile's single-binary build stays true, and there is no path
to get wrong at runtime.

**Do not reach for Prometheus + Grafana.** It would be more capable, and it is
the wrong trade on this box: the host runs the Go server *and* Postgres *and*
sits around 69% memory already. Two more containers to watch a memory-pressured
single server is a poor bargain. Revisit only if the deployment ever grows past
one machine.

### The auth pattern — get this right or the page 401s forever

A browser navigating to a URL **cannot send an `Authorization` header**. So:

- **The page itself is public.** It contains no numbers.
- **The data endpoints are staff-guarded.** The page's script signs in, stores
  the token in `localStorage`, and only then fetches.

`clubfair_dashboard.html` already implements exactly this (`TOKEN_KEY`,
`loadStats()`, `Bearer ` + token). Copy the shape. Serve the stats page under
`/su-server/` with the SU staff identity, since these numbers span all three
products but the SU staff role is the server-wide one.

Do not "fix" the public page by adding auth middleware to it. That breaks
navigation and the gate is already on the data.

## What to build

Roughly in value order. Each phase is independently useful — ship and use one
before starting the next, or you will build metrics nobody reads.

### Phase 1 — Go runtime (`GET /su-server/admin/runtime`)

Cheapest real signal. Answers "is the process itself healthy."

- `runtime.NumGoroutine()` — the important one. Long-polls park a goroutine
  each, so this tracks concurrent pollers. A number that climbs and never falls
  is a leak, most likely a `ChatWatch` that never had `Release()` called.
- `runtime.GOMAXPROCS(0)`, `runtime.NumCPU()` — confirms what the container
  actually gets, which is **not** always the host's core count.
- Heap in use, stack in use, GC pause and count, next GC target.
- Process uptime, and build info via `runtime/debug.ReadBuildInfo()` so the page
  can show which commit is live.

> **Trap:** `runtime.ReadMemStats` stops the world. It is fine at dashboard
> refresh rates and **not** fine on a per-request middleware. Prefer the
> `runtime/metrics` package if you end up sampling often.

### Phase 2 — request metrics (`GET /su-server/admin/requests`)

The one that would have pointed straight at `/clubfair/progress`.

A middleware recording, **per route pattern** (`chi.RouteContext(ctx).RoutePattern()`,
never the raw URL — `/booths/1` and `/booths/2` must not become separate rows):

- count, in-flight count
- latency p50 / p95 / p99
- status-code classes (2xx / 4xx / 5xx counts)

Keep it in memory behind a `sync.RWMutex` or atomics. No new dependency.
`schoolsCache` in `wbw_admin_handler.go` is the house pattern for
mutex-guarded shared state.

> **Traps:**
> - **Exclude the long-poll routes from latency alarms, or annotate them.** A
>   25-second `chat/sync` is *correct*, not slow. If p99 alerts treat it as a
>   problem, the dashboard trains you to ignore it. See
>   [chat-v2-deploy.md](./chat-v2-deploy.md).
> - Capture the status code with
>   `middleware.NewWrapResponseWriter(w, r.ProtoMajor)` (chi's own, in
>   `middleware/wrap_writer.go`), not a hand-rolled wrapper struct, or you will
>   silently drop `http.Flusher` and break the long-polls.
> - Mount it **after** `middleware.Recoverer` so a panicking handler still
>   records as a 500.

### Phase 3 — throttle visibility

There is a known gap here worth fixing in the same pass.

chi's `ThrottleBacklog` rejects with `http.Error` — **plain text**, and with no
`Retry-After` header. Three distinct messages, and they mean opposite things:

| Message | Meaning | Fix |
|---|---|---|
| `Server capacity exceeded.` | backlog full, refused instantly | raise `AUTH_THROTTLE_BACKLOG` |
| `Timed out while waiting for a pending request to complete.` | queued but waited the full 25 s | raise **throughput**; a bigger backlog makes this worse |
| `Context was canceled.` | client gave up mid-wait | usually not actionable |

Two things to do:

1. **Count them**, split by which message, and show them on the dashboard.
   Without the split the number is unreadable — the two real cases call for
   opposite responses.
2. **Replace the plain-text body with JSON.** CLAUDE.md forbids `http.Error`
   because the frontends cannot parse it, and a throttled client currently gets
   a body it cannot decode plus no backoff hint, which pushes it straight into a
   retry. Use `ThrottleWithOpts` with a `RetryAfterFn` and a wrapper in
   `internal/middleware` that both auth groups share.

Current settings, for reference — capacity is `limit + backlog`, so **2,540 per
group**, and the two groups are independent throttlers:

```
AUTH_THROTTLE_LIMIT       40     concurrent (bcrypt cost 10 ≈ 80 ms CPU each)
AUTH_THROTTLE_BACKLOG     2500   queued
AUTH_THROTTLE_TIMEOUT_SEC 25     then 429
```

### Phase 4 — long-poll and fan-out internals

Needs small accessor methods on the existing types; none of this is exposed yet.

- `ChatEvents` — number of waiters, and number of groups being watched. Read it
  under the existing `mu`. A waiter count that only grows means a missing
  `Release()`.
- `SOSEvents` — same.
- Listener health for both: connected or reconnecting, and the current backoff.
  `reconnectBackoff` in `wbw_sos_events.go` already models this. A silently
  disconnected listener is invisible today and degrades chat to 25-second
  latency without any error.
- `WBWPushService` — sends attempted / failed / tokens reaped.

### Phase 5 — Postgres side

The highest-value item in this whole document, and it needs no dashboard code.

**`pg_stat_statements` is not enabled.** There is no `shared_preload_libraries`
anywhere in `docker-compose.yml` and the `database` service has no `command:`
override. It would have found the `rank` query in seconds, ranked by total time
consumed, **from real traffic** — no load test, no seeding, no guessing which
endpoint to model.

To enable:

1. A `command:` on the `database` service:
   `postgres -c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track=all`
2. A migration pair doing `CREATE EXTENSION IF NOT EXISTS pg_stat_statements;`
   (and the `DROP` in the `.down.sql`).
3. Restart Postgres — it is a postmaster-level setting, not a reload.

Then surface the top statements by `total_exec_time` on the dashboard, plus
`pg_stat_activity` (active / idle / idle-in-transaction counts) and
`pg_stat_database` for commits, rollbacks and deadlocks.

> Also worth showing: `SELECT count(*) FROM pg_stat_activity` against
> `max_connections`, which is the **default 100** — nothing overrides it. The
> app currently uses at most `DB_MAX_CONNS` (20) plus two dedicated `LISTEN`
> connections.

## Refresh rate

Poll no faster than every 5 seconds, and default to 10–15. A stats page that
hammers the server distorts what it is measuring, and every fetch takes a pool
connection like any other request. Do not put the dashboard on a 1-second timer
because it looks livelier.

Prefer one endpoint returning a composite object over five parallel fetches, for
the same reason `ClubFairProgress` is one call: five requests are five chances to
render half a state.

## House rules that bite here specifically

- **`middleware.WriteJSON` / `middleware.WriteError`, never `http.Error`.**
  Errors are always `{"error": "..."}`.
- **Layer boundaries.** Stats handlers reading a live counter need no service —
  that is not a licence to put SQL in a handler. Anything hitting Postgres
  (Phase 5) goes through a repository like everything else.
- **Do not set `WriteTimeout` on the HTTP server.** It is unset on purpose for
  the 25-second long-polls, and there is a comment saying so.
- **`*pgxpool.Pool`, never `*pgx.Conn`.** `config.ConnectDB` is gone; the
  comment where it used to be explains why it must not come back.
- **Never borrow a pool connection for `LISTEN`.** Use `config.ConnectListener`.
- **New route → decide auth at the route, in `cmd/main.go`**, and say why in a
  comment if it is public. The dashboard *page* being public is exactly such a
  case and needs the comment.
- Tests: plain `testing`, `t.Fatalf`/`t.Errorf`, no `testify`. Pure arithmetic
  (percentages, percentiles, unit conversions) is worth unit-testing precisely
  because it fails silently — see `db_pool_handler_test.go`.

## Reading the numbers

Two habits that decide whether the dashboard earns its keep:

- **Counters are cumulative since boot.** A quiet morning dilutes a bad
  afternoon. Look at deltas over a window, or read during load rather than
  after. Consider showing per-second rates rather than raw totals.
- **When latency climbs, check `empty_acquire_pct` first.** Flat means the pool
  is innocent and the cost is inside Postgres — go to `pg_stat_statements`.
  Climbing means requests are queueing for connections, and *then* `DB_MAX_CONNS`
  is worth revisiting. Not before.

## Open questions

- Should the dashboard show WBW and Club Fair traffic separately? The pool is
  shared, so pool numbers cannot be split — but request metrics can, by route
  prefix, and probably should be.
- Is there an appetite for retention (a small rolling window in memory, say the
  last hour at 15-second resolution) so trends are visible, or is instantaneous
  enough? In-memory retention dies with the process, which for a single-binary
  deploy means every restart. Worth deciding before building charts.
- Nothing here alerts. If that is wanted, decide where it goes — this server has
  no notification path to staff that is not a student-facing feature.
