# แชท v2 — เช็คก่อน deploy

`/wbw/groups/{id}/chat/sync?wait=25` **ค้าง request ไว้ได้ถึง 25 วินาที** โดยตั้งใจ นั่นคือ
long-poll ไม่ใช่ request ค้าง สิ่งที่อยู่ระหว่างทางต้องยอมให้ค้างได้ ไม่งั้นแชทจะช้าเท่ารอบ
poll (25 วิ) หรือพังเป็น 502/504

วัดเมื่อ 2026-08-01 บนเครื่อง dev ทุกค่าด้านล่างเป็นค่าที่วัดจริง ไม่ใช่ค่าที่คาดว่าจะเป็น

## 1. ตัวคั่นทาง (reverse proxy / tunnel)

**แผนเดิมเขียน snippet ของ nginx ไว้ — ใช้ไม่ได้ ไม่มี nginx อยู่ในเส้นทางเลย**

```
api.studentunion.social  ->  Cloudflare  ->  cloudflared tunnel  ->  backend:8080  (Go)
wbw.sumfu.store          ->  Cloudflare  ->  Express                              (Node เดิม)
```

ยืนยันจาก response header: `server: cloudflare` ทั้งสองโดเมน ไม่มี nginx ให้ตั้ง
`proxy_buffering` ที่ไหน

ที่ต้องรู้แทน:

- Cloudflare ตัด request ที่ origin ไม่ตอบภายใน **100 วินาที** (ขึ้น error 524)
  25 วิของเราอยู่ใต้เพดานนั้นสบาย
- `cloudflared` ไม่มี response timeout เป็นค่าปริยาย (`--proxy-connect-timeout` 30 วิ
  คุมแค่ตอนเปิด connection ไม่ใช่ตอนรอ response)
- Go: `http.ListenAndServe` ใน `cmd/main.go` **ไม่ได้ตั้ง `WriteTimeout`** = ไม่มีเพดาน
  ฝั่ง server ถ้ามีใครเติม `WriteTimeout` ทีหลัง ต้องให้เกิน 25 วิ ไม่งั้น long-poll
  จะถูกตัดทุกครั้งที่ไม่มีข้อความเข้า

**ยังไม่ได้ทดสอบผ่านเส้นทางสาธารณะจริง** เพราะตอนวัด production ยังรันโค้ดเก่าที่ไม่มี
endpoint นี้ (ตอบ 404) ทดสอบทันทีหลัง deploy:

```bash
TOKEN=$(curl -s -X POST https://api.studentunion.social/wbw/auth/login \
  -H 'content-type: application/json' \
  -d '{"username":"<user>","password":"<pass>"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

time curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOKEN" \
  'https://api.studentunion.social/wbw/groups/1/chat/sync?wait=20'
```

ต้องได้ **`200` หลังผ่านไปราว 20 วินาที** — ไม่ใช่ตอบกลับทันที และไม่ใช่ 502/504/524
ตอบทันที = มีอะไรสักอย่างระหว่างทางไม่ยอมให้ค้าง ต้องหาให้เจอก่อนปล่อยผู้ใช้เข้า

ที่ยืนยันแล้วคือฝั่ง Go เอง: สโมคเทสต์เห็น `wait=25` ค้างครบ 25.0 วิแล้วตอบ 200 ว่าง
และ `wait=2` ค้าง 2.01 วิ ตรงตามที่ขอ

## 2. เพดาน file descriptor

ผู้ใช้ที่เปิดแอปอยู่ = 1 connection ค้างต่อคน ไม่ใช่ต่อ request

| | วัดได้ | เกณฑ์ |
|---|---|---|
| `su-server` (Go) | **1048576** | ≥ 65535 |
| `backend-api-1` (Node) | **1048576** | ≥ 65535 |

```bash
docker exec su-server sh -c 'ulimit -n'
```

ผ่านสบาย ไม่ต้องเติม `ulimits:` ใน `docker-compose.yml` — เกินเกณฑ์ 16 เท่า

## 3. ที่ว่างของ connection Postgres

long-poll กิน connection เฉพาะตัว **1 เส้นต่อ process** สำหรับ `LISTEN` แยกจาก pool
(ดู `config.ConnectListener` — ยืมจาก pool ไม่ได้ เพราะ connection ที่ LISTEN อยู่จะค้าง
รอ notification ตลอด กินสล็อตไปเฉยๆ)

| | วัดได้ | งบต่อ process |
|---|---|---|
| `max_connections` (ทั้งสอง DB) | **100** | — |
| ใช้อยู่ตอนวัด — SUS | 10 | — |
| ใช้อยู่ตอนวัด — Node | 16 | — |
| SUS: `DB_MAX_CONNS` ปริยาย 20 + LISTEN 1 | | **21** |
| Node: pg pool ปริยาย 10 + LISTEN 1 | | **11** |

SUS รองรับได้ **4 replica** (4 × 21 = 84 < 100 เผื่อ superuser ไว้ด้วย) ตอนนี้รัน replica
เดียว ยังห่างเพดานมาก

Node รัน process เดียว (`app.listen` เฉยๆ ไม่มี `cluster`) = 11 เส้น

ถ้าจะเพิ่ม replica ต้องคุมด้วย `DB_MAX_CONNS`:

```
(จำนวน replica) × (DB_MAX_CONNS + 1) ≤ max_connections − เผื่อ superuser
```

```bash
docker exec postgres-db sh -c 'psql -U $POSTGRES_USER -d $POSTGRES_DB -tAc "show max_connections"'
docker exec postgres-db sh -c 'psql -U $POSTGRES_USER -d $POSTGRES_DB -tAc "select count(*) from pg_stat_activity"'
```

## 4. เลข migration — ห้ามเว้นช่อง

deploy รอบนี้พา migration **3 ตัว** ไม่ใช่ตัวเดียว: prod อยู่ที่ version 8 แต่ main มีถึง 10
อยู่แล้ว บวกของแชทเป็น 11 → รัน 9, 10, 11 รวดเดียว แปลว่า **ฟีเจอร์ booth จะขึ้นพร้อมแชท**
ถ้าไม่ต้องการแบบนั้นต้องแยกรอบ deploy

```bash
docker compose run --rm migrate version    # ก่อน deploy ต้องได้ 8 · หลัง deploy ต้องได้ 11
```

`migrate` service รันก่อน `backend` เสมอ (`service_completed_successfully`) **ถ้า migrate
ล้ม backend จะไม่สตาร์ทเลย**

ห้ามปล่อยให้เลข migration มีช่องว่างเด็ดขาด ทดสอบแล้ว: golang-migrate ที่ขึ้นไปเลขสูงกว่า
แล้วจะ**ไม่ย้อนกลับมารัน migration ที่เพิ่งโผล่มาเติมช่อง** มันตอบ `no change` เฉยๆ แล้ว
ตารางนั้นไม่ถูกสร้างตลอดไป โดยไม่มี error ให้เห็น (branch `check-ins` ยังจองเลข 11/12 ไว้
ต้องเลื่อนเป็น 12/13 ก่อน merge)

## 5. push (FCM)

ไม่มี `GOOGLE_APPLICATION_CREDENTIALS` = push ปิดเงียบ แชทในแอปทำงานครบทุกอย่าง ยกเว้น
การเด้งเตือนตอนปิดแอป log จะขึ้นตอนบูต:

```
WARN push ปิดอยู่: ไม่มี GOOGLE_APPLICATION_CREDENTIALS — แชทในแอปยังทำงานปกติ
```

อยากเปิดก็วาง service account ไว้แล้วชี้ env นี้ให้ตรง จะได้ `INFO push พร้อมใช้งาน
(FCM HTTP v1)` แทน · **ยังไม่เคยทดสอบส่ง FCM จริง** เพราะเครื่อง dev ไม่มี service account

## หมายเหตุ Android

แอป Android ยังใช้ `/groups/:id/messages` แบบเดิม ไม่เคยยิง `/chat/read` → cursor ค้างที่ 0
→ คนที่ใช้ Android จะ**ไม่ถูกนับใน "อ่านแล้ว N"** จนกว่าจะ mirror ฝั่ง Android ตามทีหลัง

และ Android ชี้ `wbw.sumfu.store` (Node) ไม่ได้แตะ SUS เลย — deploy นี้จึงไม่กระทบ Android
