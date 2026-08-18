# ============================================================
# su-server — task runner
#
# `make` on its own prints every target. Nothing in the build depends on this
# file: it wraps commands you would otherwise type, so a broken target here can
# never break the server.
#
# The API targets are httpie one-liners for poking a running server by hand.
# They are not tests — they print whatever comes back.
# ============================================================

# Leading "-" so a missing .env is not a fatal error and `make help` still
# works. Targets that genuinely need a value check for it themselves.
-include .env
export

.DEFAULT_GOAL := help

# ---------- where to send requests ----------
#
# Built from SERVER_HOST/SERVER_PORT and NOT from BASE_URL_DEVELOPMENT, which
# cannot be trusted to mean one thing: .env.example has it as the bare origin
# while at least one .env in circulation has "/su-server" already on the end.
# Every target below appends its own prefix, so the base here must be the
# origin and nothing more. (Nothing in Go reads BASE_URL_DEVELOPMENT at all —
# it was only ever a Makefile variable.)
#
# Override per invocation to hit another box or the tunnel:
#   make get-events API=https://api.studentunion.social
SERVER_HOST ?= localhost
SERVER_PORT ?= 8080
API ?= http://$(SERVER_HOST):$(SERVER_PORT)

SU  := $(API)/su-server
WBW := $(API)/wbw
CF  := $(API)/clubfair

# ---------- auth ----------
#
# Almost every route needs a bearer token now. Get one with `make wbw-login`
# (WBW) or from the Google sign-in flow (SU / Club Fair), then pass it in:
#   make get-user id=1 TOKEN=eyJhbGci...
# Left empty the header is simply omitted, so the route answers 401 rather than
# sending "Bearer " and looking like a token problem.
TOKEN ?=
AUTH = $(if $(TOKEN),Authorization:"Bearer $(TOKEN)")

# ---------- database ----------
#
# These drive the LOCAL `migrate` CLI against the DB published on 127.0.0.1.
# Compose runs the same files through its own `migrate` service on every boot,
# so the two stay in sync whichever way you apply them.
#
# ⚠ DB_PASS goes into a URL unescaped. A password containing @ : / or ? breaks
# the parse — quote it out of existence or use a plain alphanumeric one.
DB_PORT ?= 5432
DB_MIGRATION_PATH ?= db/migrations
DB_URL := postgres://$(DB_USER):$(DB_PASS)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable

# The compose service name, for `docker exec`. Matches container_name in
# docker-compose.yml.
PG_CONTAINER ?= postgres-db

# require,<var>,<usage> — fail with a usable message instead of firing a request
# at a URL with an empty path segment and getting a puzzling 404.
define require
	@test -n "$($(1))" || { echo "usage: $(2)" >&2; exit 1; }
endef

# ============================================================
##@ Help
# ============================================================

.PHONY: help print-env

help: ## Print this list
	@awk 'BEGIN {FS = ":.*##"; printf "\nusage: make \033[36m<target>\033[0m [VAR=value]\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		END { print "" }' $(MAKEFILE_LIST)

print-env: ## Show the resolved config (password masked)
	@echo "API:  $(API)"
	@echo "DB:   $(DB_USER)@localhost:$(DB_PORT)/$(DB_NAME)"
	@echo "PASS: $(if $(DB_PASS),(set),(EMPTY))"
	@echo "TOKEN:$(if $(TOKEN), (set), (empty — API targets will 401))"

# ============================================================
##@ Dev
# ============================================================

.PHONY: dev watch build fmt vet test test-db check

dev: ## Run the server (go run cmd/main.go)
	go run cmd/main.go

watch: ## Run with hot reload (air)
	air

build: ## Compile to bin/main
	go build -o bin/main cmd/main.go

fmt: ## gofmt every package
	go fmt ./...

vet: ## go vet every package
	go vet ./...

test: ## Unit tests. No database needed — the DB-backed ones skip
	go test ./...

# The tests this switch turns on prove database behaviour: row locks, CHECK
# constraints, transactions. A fake pool has none of those, so they run against
# a real Postgres or not at all.
#
# ⚠ They WRITE to whatever .env points at. Point it at dev.
test-db: ## Tests that need a real Postgres (WBW_DB_TESTS=1)
	WBW_DB_TESTS=1 go test ./... -count=1

check: fmt vet test ## fmt + vet + test, what CI would run

# ============================================================
##@ Docker
# ============================================================

.PHONY: up down restart logs ps psql

# Public ingress is the NAMED Cloudflare tunnel at api.studentunion.social,
# configured in the Cloudflare dashboard, not here. Port 8080 is bound to
# 127.0.0.1, so the stack is reachable from this box and through the tunnel —
# and from nowhere else.
up: ## Build and start the stack in the background
	docker compose up -d --build

down: ## Stop the stack
	docker compose down

restart: ## Rebuild and restart the backend alone
	docker compose up -d --build backend

logs: ## Follow the backend log
	docker compose logs -f backend

ps: ## What is running
	docker compose ps

psql: ## Open a psql shell on the database
	docker exec -it $(PG_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME)

# ============================================================
##@ Migrations
# ============================================================

.PHONY: migrate-up migrate-down migrate-new migrate-version migrate-force migrate-drop tables

migrate-up: ## Apply every pending migration
	migrate -path $(DB_MIGRATION_PATH) -database "$(DB_URL)" up

# `make migrate-down` rolls back one, `make migrate-down N=3` rolls back three.
migrate-down: ## Roll back N migrations (default 1)
	migrate -path $(DB_MIGRATION_PATH) -database "$(DB_URL)" down $(or $(N),1)

# Creates the .up.sql and .down.sql pair, numbered next in sequence. Both
# files always exist, even when the down is a one-line DROP.
migrate-new: ## Create a migration pair: make migrate-new name=add_thing
	$(call require,name,make migrate-new name=add_thing)
	migrate create -ext sql -dir $(DB_MIGRATION_PATH) -seq $(name)

# A "(dirty)" suffix means a migration aborted partway. golang-migrate then
# refuses every later run — including the one that would fix it — and since the
# backend waits on the migrate service, the whole stack stops coming up.
migrate-version: ## Which migration the DB thinks it is on
	migrate -path $(DB_MIGRATION_PATH) -database "$(DB_URL)" version

# Clears a dirty flag by stamping the version the schema REALLY matches. It runs
# no SQL of its own — only rewrites schema_migrations — so check what the schema
# actually contains first, then re-run migrate-up.
migrate-force: ## Stamp a version: make migrate-force V=8
	$(call require,V,make migrate-force V=8)
	migrate -path $(DB_MIGRATION_PATH) -database "$(DB_URL)" force $(V)

migrate-drop: ## Drop EVERY table in the database (prompts first)
	migrate -path $(DB_MIGRATION_PATH) -database "$(DB_URL)" drop

tables: ## List tables
	docker exec -it $(PG_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "\dt"

# ============================================================
##@ WBW (เดินรอบดอย)
# ============================================================

.PHONY: wbw-admin wbw-login wbw-me wbw-capacity wbw-schools wbw-check-schools

# บัญชีผู้ดูแลคนแรก — ถ้ามี username นี้อยู่แล้วจะเปลี่ยนรหัสผ่านให้แทน
wbw-admin: ## Create the first admin: make wbw-admin user=admin pass=... name="ชื่อ"
	$(call require,user,make wbw-admin user=admin pass=secret name="ชื่อที่แสดง")
	$(call require,pass,make wbw-admin user=admin pass=secret name="ชื่อที่แสดง")
	go run cmd/createadmin/main.go "$(user)" "$(pass)" "$(name)"

# พิมพ์ token ออกมาอย่างเดียว เอาไปใส่ TOKEN= ของ target อื่นได้เลย
wbw-login: ## Log in, print the token: make wbw-login user=... pass=...
	$(call require,user,make wbw-login user=6531503001 pass=secret)
	$(call require,pass,make wbw-login user=6531503001 pass=secret)
	@http --print=b POST $(WBW)/auth/login username="$(user)" password="$(pass)"

wbw-me: ## Own profile
	http GET $(WBW)/me $(AUTH)

# ที่นั่งคงเหลือ — เปิดสาธารณะ ไม่ต้องมี token
wbw-capacity: ## Seats left in the 2,000-participant cap
	http GET $(WBW)/capacity

# เปิดสาธารณะ หน้าสมัครเรียกก่อนล็อกอิน
wbw-schools: ## School list as the registration form sees it
	http GET $(WBW)/admin/schools

# ตรวจว่า school_id ตรงกับที่ frontend hardcode ไว้
# (web-next/components/register/mfu-data.ts)
wbw-check-schools: ## school_id straight from the database
	docker exec -it $(PG_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) \
		-c "SELECT school_id, name FROM school ORDER BY school_id;"

# ============================================================
##@ SU API — events
# ============================================================

.PHONY: get-events get-event create-event update-event delete-event

get-events: ## List events
	http GET $(SU)/events

get-event: ## One event: make get-event id=1
	$(call require,id,make get-event id=1)
	http GET $(SU)/events/$(id)

create-event: ## Create an event (staff token)
	http POST $(SU)/events $(AUTH) \
		title="$(title)" \
		content="$(content)" \
		location="$(location)" \
		date="$(date)" \
		time="$(time)" \
		link="$(link)"

update-event: ## Update an event (staff token): make update-event id=1 title=...
	$(call require,id,make update-event id=1 title="..." content="...")
	http PUT $(SU)/events/$(id) $(AUTH) \
		title="$(title)" \
		content="$(content)"

delete-event: ## Delete an event (staff token): make delete-event id=1
	$(call require,id,make delete-event id=1)
	http DELETE $(SU)/events/$(id) $(AUTH)

# ============================================================
##@ SU API — users
# ============================================================

.PHONY: get-user get-user-email insert-user upsert-user update-user

# GET/PATCH on a user are self-or-staff: the token must belong to that id, or
# to a staff account.
get-user: ## One user: make get-user id=1
	$(call require,id,make get-user id=1)
	http GET $(SU)/users/$(id) $(AUTH)

# Staff only — knowing an address must not hand over the profile behind it.
get-user-email: ## By email (staff token): make get-user-email email=...
	$(call require,email,make get-user-email email=640@lamduan.mfu.ac.th)
	http GET $(SU)/users/email/$(email) $(AUTH)

insert-user: ## Create a user (staff token)
	http POST $(SU)/users/insert $(AUTH) \
		name="$(name)" \
		email="$(email)" \
		usertype="$(usertype)" \
		student_id="$(student_id)" \
		major="$(major)" \
		school="$(school)" \
		avatar_url="$(avatar_url)" \
		oauth_subject="$(oauth_subject)"

upsert-user: ## Create or update a user (staff token)
	http POST $(SU)/users/upsert $(AUTH) \
		name="$(name)" \
		email="$(email)" \
		usertype="$(usertype)" \
		student_id="$(student_id)" \
		major="$(major)" \
		school="$(school)" \
		avatar_url="$(avatar_url)" \
		oauth_subject="$(oauth_subject)"

update-user: ## Update a profile: make update-user id=1 major=...
	$(call require,id,make update-user id=1 major="..." school="...")
	http PATCH $(SU)/users/$(id) $(AUTH) \
		major="$(major)" \
		school="$(school)" \
		student_id="$(student_id)"

# ============================================================
##@ SU API — steps, leaderboard, booths
# ============================================================

.PHONY: get-steps get-steps-range sync-steps sync-steps-bulk
.PHONY: get-leaderboard get-user-rank update-leaderboard reset-leaderboard get-booths

get-steps: ## A user's steps (self or staff): make get-steps userID=1
	$(call require,userID,make get-steps userID=1)
	http GET $(SU)/steps/$(userID) $(AUTH)

get-steps-range: ## By date range: make get-steps-range userID=1 from=... to=...
	$(call require,userID,make get-steps-range userID=1 from=2026-06-16 to=2026-06-22)
	http GET "$(SU)/steps/$(userID)/range?from=$(from)&to=$(to)" $(AUTH)

# ⚠ Both sync routes still take user_id from the BODY rather than the token —
# a signed-in caller can write to another id. Documented in CLAUDE.md.
sync-steps: ## Sync one day (sample payload)
	http POST $(SU)/steps/sync $(AUTH) \
		user_id:=16 \
		step_count:=8432 \
		recorded_date="2026-06-22"

sync-steps-bulk: ## Sync several days (sample payload)
	http POST $(SU)/steps/sync/bulk $(AUTH) \
		Content-Type:application/json \
		:='[{"user_id":16,"step_count":8432,"recorded_date":"2026-06-22"},{"user_id":16,"step_count":12043,"recorded_date":"2026-06-21"},{"user_id":16,"step_count":6721,"recorded_date":"2026-06-20"}]'

# The two reads are public on purpose — a leaderboard is the campaign's front page.
get-leaderboard: ## Full ranked leaderboard
	http GET $(SU)/leaderboard

get-user-rank: ## One user's rank: make get-user-rank userID=1
	$(call require,userID,make get-user-rank userID=1)
	http GET $(SU)/leaderboard/$(userID)

update-leaderboard: ## Set a step count (staff token): make update-leaderboard userID=1 step_count=8432
	$(call require,userID,make update-leaderboard userID=1 step_count=8432)
	http POST $(SU)/leaderboard/update $(AUTH) \
		user_id:=$(userID) \
		step_count:=$(step_count)

reset-leaderboard: ## Reset the leaderboard (staff token)
	http POST $(SU)/leaderboard/reset $(AUTH)

get-booths: ## Booth directory (any SU token). `secret` is never returned
	http GET $(SU)/booths $(AUTH)

# ============================================================
##@ Club Fair
#
# These 404 unless CLUBFAIR_JWT_SECRET is set — with no signing key the routes
# are deliberately left unregistered rather than served without security.
# ============================================================

.PHONY: cf-login cf-booths cf-zones cf-me cf-progress cf-info cf-program cf-prizes
.PHONY: cf-staff cf-participants cf-admin-prizes cf-admin-program
.PHONY: cf-staff-list cf-new-account cf-assign-booths cf-my-booths

cf-login: ## Log in, print the token: make cf-login phone=... pass=...
	$(call require,phone,make cf-login phone=0812345678 pass=secret)
	$(call require,pass,make cf-login phone=0812345678 pass=secret)
	@http --print=b POST $(CF)/auth/login phone="$(phone)" password="$(pass)"

cf-booths: ## The 28 clubs in floor-plan order (public)
	http GET $(CF)/booths

cf-zones: ## The three areas in signage order (public)
	http GET $(CF)/zones

cf-me: ## Own profile
	http GET $(CF)/me $(AUTH)

cf-progress: ## Count, visited booths, rank and prize tiers
	http GET $(CF)/progress $(AUTH)

# The three that exist so no client has to hold its own copy of this data.
cf-info: ## When and where the fair is (public)
	http GET $(CF)/info

cf-program: ## The published running order (public)
	http GET $(CF)/program

cf-prizes: ## The tiers a student can still reach (public)
	http GET $(CF)/prizes

# ---------- staff dashboard ----------
#
# Everything under /clubfair/admin needs a token whose cf_role is staff or
# admin. There is no way to mint the first one from the web — see cf-staff.

# Promotes an account that already exists. The person has to sign in once
# first: this command does not create a row, because inventing an email would
# create an account their real Google sign-in would never find.
cf-staff: ## Promote an account: make cf-staff email=... role=admin
	$(call require,email,make cf-staff email=6731503001@lamduan.mfu.ac.th role=admin)
	go run cmd/createclubfairstaff/main.go "$(email)" "$(or $(role),admin)"

cf-participants: ## The roster (staff token): make cf-participants q=somchai
	http GET "$(CF)/admin/participants?q=$(q)" $(AUTH)

cf-admin-prizes: ## Tiers with claim counts and retired ones (staff token)
	http GET $(CF)/admin/prizes $(AUTH)

cf-staff-list: ## Just the people who run the fair (staff token)
	http GET "$(CF)/admin/participants?role=booth_owner,staff,admin" $(AUTH)

# Creating an account above `student` is admin-only, matching the rule for
# promoting one — they hand out the same thing. There is no invitation email,
# so whoever runs this has to pass the password on.
cf-new-account: ## make cf-new-account email=... pass=... role=booth_owner name=... surname=...
	$(call require,email,make cf-new-account email=6800000002@lamduan.mfu.ac.th pass=booth12345 role=booth_owner)
	$(call require,pass,make cf-new-account email=6800000002@lamduan.mfu.ac.th pass=booth12345 role=booth_owner)
	@http --print=b POST $(CF)/admin/participants $(AUTH) \
	    first_name="$(or $(name),Booth)" surname="$(or $(surname),Owner)" \
	    email="$(email)" role="$(or $(role),booth_owner)" password="$(pass)"

# Replaces the whole set — the ids sent are the ids the owner ends up with.
# Sending an empty list is how a booth owner is locked out of every screen, and
# unlike suspending an account it takes effect on the next poll.
cf-assign-booths: ## make cf-assign-booths id=28 booths=17,18
	$(call require,id,make cf-assign-booths id=28 booths=17,18)
	@http --print=b PUT $(CF)/admin/participants/$(id)/booths $(AUTH) \
	    booth_ids:="[$(booths)]"

cf-my-booths: ## The booths the token's own account runs
	http GET $(CF)/me/booths $(AUTH)

cf-admin-program: ## The running order, drafts included (staff token)
	http GET $(CF)/admin/program $(AUTH)
