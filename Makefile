include .env
export

.PHONY: dev build up down logs test-health test-events

print-env:
	@echo "USER: $(DB_USER)"
	@echo "PASS: $(DB_PASS)"
	@echo "DB: $(DB_NAME)"

# Docker
#
# Two ways in, selected by compose profile:
#   up-lan     Caddy on :80 — reachable from any machine on this network at
#              http://<this-box-ip>/su-server/...
#   up-remote  Cloudflare quick tunnel — a public https URL, no domain needed.
#              Run `make tunnel-url` after; it changes on every restart.
# Both can run at once (up-all). Bare `up` starts only db + backend, which are
# reachable from this box alone since 8080 is bound to loopback.
up:
	docker compose up --build

up-lan:
	docker compose --profile lan up -d --build

up-remote:
	docker compose --profile remote up -d --build

up-all:
	docker compose --profile lan --profile remote up -d --build

# Quick-tunnel URLs are only ever printed to the log, never stored.
tunnel-url:
	@docker compose logs cloudflared \
		| grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' \
		| tail -1 \
		|| echo "no URL yet — is the remote profile running?"

# --profile flags are needed here too, or profiled services are left running.
down:
	docker compose --profile lan --profile remote down

logs:
	docker compose logs -f backend

# Dev
dev:
	go run cmd/main.go

watch:
	air

build:
	go build -o bin/main cmd/main.go

# migrations

migrate-up:
	migrate -path $(DB_MIGRATION_PATH) -database "postgres://$(DB_USER):$(DB_PASS)@localhost:5432/$(DB_NAME)?sslmode=disable" up

migrate-down:
	migrate -path $(DB_MIGRATION_PATH)/$(FILE) -database "postgres://$(DB_USER):$(DB_PASS)@localhost:5432/$(DB_NAME)?sslmode=disable" down

migrate-drop:
	migrate $(DB_MIGRATION_PATH)/$(FILE) -database "postgres://$(DB_USER):$(DB_PASS)@localhost:5432/$(DB_NAME)?sslmode=disable" drop

tables:
	docker exec -it postgres-db psql -U $(DB_USER) -d $(DB_NAME) -c "\dt"

# ==========================================
# WBW (เดินรอบดอย)
# ==========================================

# สร้างผู้ดูแลคนแรก: make wbw-admin user=admin pass=yourpassword name="ชื่อที่แสดง"
wbw-admin:
	go run cmd/createadmin/main.go $(user) $(pass) $(name)

wbw-schools:
	http GET $(BASE_URL_DEVELOPMENT)/../wbw/admin/schools

# ตรวจว่า school_id ตรงกับที่ frontend hardcode ไว้ (web-next/components/register/mfu-data.ts)
wbw-check-schools:
	docker exec -it postgres-db psql -U $(DB_USER) -d $(DB_NAME) -c "SELECT school_id, name FROM school ORDER BY school_id;"

check-schema-migrations:
	docker exec -it postgres-db psql -U $(DB_USER) -d $(DB_NAME) -c "SELECT * FROM schema_migrations;" \dt


# Routes

#==========================================
# Event
# =========================================

get-events:
	http GET $(BASE_URL_DEVELOPMENT)/events
 
get-event:
	http GET $(BASE_URL_DEVELOPMENT)/events/$(id)
 
create-event:
	http POST $(BASE_URL_DEVELOPMENT)/events \
		title="$(title)" \
		content="$(content)" \
		location="$(location)" \
		date="$(date)" \
		time="$(time)" \
		link="$(link)"

update-event:
	http PUT $(BASE_URL_DEVELOPMENT)/events/$(id) \
		title=$(title) \
		content=$(content)
 
delete-event:
	http DELETE $(BASE_URL_DEVELOPMENT)/events/$(id)


#==========================================
# User
# =========================================

create-user:
	http POST $(BASE_URL_DEVELOPMENT)/users \
		name="$(name)" \
		email="$(email)" \
		usertype="$(usertype)" \
		student_id="$(student_id)" \
		major="$(major)" \
		school="$(school)" \
		avatar_url="$(avatar_url)" \
		oauth_subject="$(oauth_subject)"

get-user:
	http GET $(BASE_URL_DEVELOPMENT)/users/$(id)

get-user-email:
	http GET $(BASE_URL_DEVELOPMENT)/users/email/$(email)

update-user:
	http PATCH $(BASE_URL_DEVELOPMENT)/users/$(id) \
		major="$(major)" \
		school="$(school)" \
		student_id="$(student_id)"

# ==========================================
# Steps
# ==========================================

sync-steps:
	http POST $(BASE_URL_DEVELOPMENT)/steps/sync \
		user_id:=16 \
		step_count:=8432 \
		recorded_date="2026-06-22"

sync-steps-bulk:
	http POST $(BASE_URL_DEVELOPMENT)/steps/sync/bulk \
		Content-Type:application/json \
		:='[{"user_id":16,"step_count":8432,"recorded_date":"2026-06-22"},{"user_id":16,"step_count":12043,"recorded_date":"2026-06-21"},{"user_id":16,"step_count":6721,"recorded_date":"2026-06-20"}]'

get-steps:
	http GET $(BASE_URL_DEVELOPMENT)/steps/$(userID)

get-steps-range:
	http GET "$(BASE_URL_DEVELOPMENT)/steps/$(userID)/range?from=$(from)&to=$(to)"

#==========================================
# Leaderboard
# ==========================================

get-leaderboard:
	http GET $(BASE_URL_DEVELOPMENT)/leaderboard

get-user-rank:
	http GET $(BASE_URL_DEVELOPMENT)/leaderboard/$(userID)

update-leaderboard:
	http POST $(BASE_URL_DEVELOPMENT)/leaderboard/update \
		user_id:=$(userID) \
		step_count:=$(step_count)

reset-leaderboard:
	http POST $(BASE_URL_DEVELOPMENT)/leaderboard/reset
