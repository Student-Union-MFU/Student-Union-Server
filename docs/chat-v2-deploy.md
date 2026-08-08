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

**วัดผ่านเส้นทางสาธารณะจริงแล้วเมื่อ 2026-08-09** (ก่อนหน้านี้วัดไม่ได้ เพราะ production ยังรัน
โค้ดเก่าที่ไม่มี endpoint นี้ ตอบ 404)

| | status | เวลาที่ใช้ |
|---|---|---|
| `wait=20` | 200 | **20.237 วิ** |
| `wait=2` | 200 | **2.229 วิ** |

overhead ~0.23 วิเท่ากันทั้งสองรอบ = round-trip ปกติ · ต้องวัดทั้งสองค่า ไม่ใช่ค่าเดียว เพราะ
`wait=2` ที่ค้าง 2 วิเป๊ะคือสิ่งที่พิสูจน์ว่าค่านี้เดินทางถึง handler จริง ไม่ได้โดน strip กลางทาง
(ถ้าโดน strip ทั้งสองรอบจะใช้เวลาเท่ากัน)

รันซ้ำได้ด้วยชุดคำสั่งนี้ — บัญชีที่ใช้ **ต้องอยู่ในกลุ่มแล้ว** (ไม่ใช่สมาชิก = 403) และต้องส่ง `after`
เป็น id ล่าสุด ไม่งั้น query แรกเจอข้อความค้างอยู่แล้วตอบทันที ซึ่งถูกต้องแต่ไม่ได้วัด long-poll:

```bash
B=https://api.studentunion.social/wbw
read -p "username: " U && read -s -p "password: " P && echo
BODY=$(U="$U" P="$P" python3 -c 'import json,os;print(json.dumps({"username":os.environ["U"],"password":os.environ["P"]}))')
TOKEN=$(curl -s -X POST "$B/auth/login" -H 'content-type: application/json' -d "$BODY" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
unset P BODY

GID=$(curl -s -H "Authorization: Bearer $TOKEN" "$B/me" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("group_id") or 0)')
AFTER=$(curl -s -H "Authorization: Bearer $TOKEN" "$B/groups/$GID/chat/sync?wait=0" \
  | python3 -c 'import sys,json;m=json.load(sys.stdin)["messages"];print(m[-1]["id"] if m else 0)')

time curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOKEN" \
  "$B/groups/$GID/chat/sync?wait=20&after=$AFTER"
time curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOKEN" \
  "$B/groups/$GID/chat/sync?wait=2&after=$AFTER"
```

ตอบทันทีทั้งสองรอบ = มีอะไรสักอย่างระหว่างทางไม่ยอมให้ค้าง · 502/504/524 = โดนตัดกลางคัน

ฝั่ง Go เองยืนยันแยกไว้ตั้งแต่แรก: สโมคเทสต์เห็น `wait=25` ค้างครบ 25.0 วิแล้วตอบ 200 ว่าง
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

deploy รอบแชท v2 พา migration **3 ตัว** ไม่ใช่ตัวเดียว: prod อยู่ที่ version 8 แต่ main มีถึง 10
อยู่แล้ว บวกของแชทเป็น 11 → รัน 9, 10, 11 รวดเดียว แปลว่า **ฟีเจอร์ booth จะขึ้นพร้อมแชท**
ถ้าไม่ต้องการแบบนั้นต้องแยกรอบ deploy · (2026-08-09: prod ขึ้นถึง **15** แล้ว พา SOS ไปด้วย
ด้วยเหตุผลเดียวกัน)

เช็คเลขปัจจุบัน — **ห้ามใช้ `docker compose run --rm migrate version`** ที่เคยเขียนไว้ตรงนี้
มันใช้ไม่ได้มาแต่ต้น: อาร์กิวเมนต์ที่ต่อท้ายไป**แทนที่ `command:` ทั้งก้อน** ซึ่งเป็นที่เก็บ
`-path` กับ `-database` อยู่ พอ flag หายหมด CLI เลยตอบ
`failed to parse scheme from source URL: URL cannot be empty` ทั้งที่ DB ปกติดี

```bash
set -a && . .env && set +a
docker exec postgres-db psql -U "$DB_USER" -d "$DB_NAME" -tAc \
  'select version, dirty from schema_migrations'
```

ได้ `15|f` = อยู่ที่ 15 และไม่ dirty · `t` แปลว่า migration พังกลางคัน ต้องเคลียร์ก่อน
(ดู `make migrate-force`) ไม่งั้นทุกรอบถัดไปจะปฏิเสธ

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
(FCM HTTP v1) project=wbw-doi` แทน

**ทดสอบส่ง FCM จริงแล้วเมื่อ 2026-08-09** — ข้อความจาก `POST /groups/1/messages` เด้งถึง
เครื่อง iOS จริงภายในไม่กี่วินาที ครบเส้น `su-server → FCM HTTP v1 → APNs → เครื่อง`

service account ต้องมาจาก Firebase project **`wbw-doi`** เท่านั้น — ตัวเดียวกับที่ทั้งสองแอปใช้
(`android_native/app/google-services.json`, `WBW/GoogleService-Info.plist`) ผิด project =
FCM ตอบ error ที่ไม่ได้บอกตรงๆ ว่าสาเหตุคืออะไร เช็คก่อนวางไฟล์:

```bash
python3 -c "import json;print(json.load(open('secrets/firebase-adminsdk.json'))['project_id'])"
# ต้องได้ wbw-doi
chmod 600 secrets/firebase-adminsdk.json   # ข้างในเป็น private key
```

### อ่าน log ยังไง

`sendChat` **ไม่มี log ตอนสำเร็จ** — เงียบ = ผ่าน · ที่จะเห็นมีสามแบบเท่านั้น

| log | หมายถึง |
|---|---|
| `push หนึ่งเครื่องล้มเหลว` | ยิงเครื่องนั้นไม่ผ่าน เครื่องอื่นในรอบเดียวกันยังไปต่อ |
| `เก็บกวาด device token ที่ใช้ไม่ได้` | FCM ตีกลับว่า token ตาย ลบออกจาก `device_token` แล้ว |
| `ส่ง push แชทไม่สำเร็จ` | พังทั้งรอบ (เช่นขอ access token ไม่ได้) |

ระวังตีความ: เงียบแปลว่า **FCM รับ token ไว้แล้ว** ไม่ได้แปลว่าถึงเครื่อง — APNs ยังดรอปทีหลัง
ได้จาก APNs key ที่ยังไม่อัปโหลด, entitlement ไม่ตรง, หรือผู้ใช้ปิดแจ้งเตือน ต้องดูที่เครื่องจริง

### ตรวจว่าใครจะได้ push ก่อนยิงจริง

query นี้เลียนแบบ `ChatPushTargets` (ตัดคนส่ง + ตัดคนที่ `read_at` ใหม่กว่า 25 วิ) ใช้ยืนยันว่า
คนที่กำลังเปิดจอแชทค้างอยู่ถูกตัดออกจริง ก่อนจะไปทดสอบกับเครื่อง

```bash
set -a && . .env && set +a
docker exec postgres-db psql -U "$DB_USER" -d "$DB_NAME" -c "
  SELECT u.username, count(d.token) AS devices,
         (s.read_at IS NULL OR now()-s.read_at > interval '25 seconds') AS gets_push
    FROM participant_profile p
    JOIN wbw_user u ON u.user_id = p.user_id
    JOIN device_token d ON d.user_id = p.user_id
    LEFT JOIN group_chat_state s ON s.user_id = p.user_id AND s.group_id = p.group_id
   WHERE p.group_id = 1 AND u.username <> '<ผู้ส่ง>'
   GROUP BY u.username, s.read_at;"
```

2026-08-09: เครื่อง iOS ที่เปิดจอแชทค้างไว้ให้ `gets_push = f` ตามที่ออกแบบ — heartbeat
(`/chat/read` ทุก 10 วิ) ดัน `read_at` สดจริงในฐานข้อมูล

## 6. heartbeat ต้องไม่ปลุก long-poll ของทั้งกลุ่ม

`/chat/read` ทำสองหน้าที่พร้อมกัน: ขยับ cursor "อ่านถึงไหน" และเป็น heartbeat "กำลังเปิดจอแชทอยู่"
แอปยิงซ้ำค่าเดิมทุก 10 วิเพื่อหน้าที่หลัง · ถ้า handler ปลุก long-poll ทุกครั้งที่ถูกเรียก heartbeat
ของทุกคนจะไปเตะทุกคน = `~(G/10)×(G−1)` ครั้ง/วิ/กลุ่ม เทียบกับที่ควรเป็น `~G/25` (ที่ 50 คน
คือ ~245 เทียบ ~2 ราว 125 เท่า) กินคิว query ของ pool จนคำขออื่นอดตามไปด้วย

`MarkRead` จึงคืน `advanced` มาด้วย และ service ปลุกเฉพาะตอน cursor ขยับจริง

### วิธีวัด — ต้องใช้กลุ่มว่างเท่านั้น

**ห้ามวัดบนกลุ่มที่มีสมาชิกจริง** ลองแล้วอ่านผลไม่ได้: ใครเข้า/ออกกลุ่ม หรือขยับ cursor จริง
ก็ยิง `NotifyGroup` ปลุกทุกคนตามดีไซน์ แยกไม่ออกว่าคนที่รออยู่ตื่นเพราะอะไร (รอบแรกวัดบนกลุ่ม 1
ได้ 16.9 วิแทนที่จะเป็น 20 เพราะเหตุนี้)

หากลุ่มว่างด้วย `SELECT group_id FROM participant_group WHERE member_count = 0` แล้วเอาบัญชี
ทดสอบสองตัวเข้าไป (`student_id` ต้องตรง `^693\d{7}$` และรหัสยาว ≥ 8) · A ค้าง long-poll
`wait=20` ส่วน B เป็นคนยิง `/chat/read`

**ต้องมีเฟส 0 เป็นตัวคุมเสมอ** ไม่งั้นแยกไม่ออกระหว่าง "แก้ได้ผล" กับ "กลุ่มเงียบบังเอิญ"

| เฟส | B ทำอะไร | ต้องได้ | วัดได้จริง 2026-08-09 |
|---|---|---|---|
| 0 | ไม่ทำอะไรเลย | ~20 วิ (หมดเวลา) | **20.237** |
| 1 | ยิงค่าเดิม 5 ครั้งที่ t=2,5,8,11,14 | ~20 วิ (ไม่ถูกปลุก) | **20.215** |
| 2 | ยิงค่าที่สูงขึ้นที่ t=3 | ~3 วิ (ถูกปลุก) | **3.235** |

เฟส 2 สำคัญเท่าเฟส 1 — ถ้ามีแต่เฟส 1 แล้วค้างครบ ก็ยังแยกไม่ออกว่าแก้ถูกหรือ `pg_notify`
พังทั้งเส้น · เฟส 2 พิสูจน์ว่าเส้นทางปลุกยังทำงาน แค่ heartbeat ไม่ผ่านประตูแล้ว

## หมายเหตุ Android

เดิมแอป Android ชี้ `wbw.sumfu.store` (Node) ไม่ได้แตะ SUS เลย และยังใช้ `/groups/:id/messages`
แบบเดิม ไม่เคยยิง `/chat/read` → cursor ค้างที่ 0 → คนที่ใช้ Android **ไม่ถูกนับใน "อ่านแล้ว N"**
และโดน push ทั้งที่เปิดจอแชทค้างอยู่

2026-08-09: Android ย้ายมา SUS แล้ว (`Weerapong-gui/WBW` PR #2) พร้อมย้ายแชทมา v2 ครบ —
long-poll, read cursor, heartbeat · แปลว่า **โหลดของ Android มากอง SUS ทั้งหมด** ไม่กระจาย
สองฝั่งเหมือนเดิมอีกแล้ว ตัวเลขในหัวข้อ 2 กับ 3 (file descriptor, connection ของ Postgres)
ต้องวัดใหม่หลังแอปเวอร์ชันนั้นกระจายครบ

Node (`wbw.sumfu.store`) เลิกใช้ได้เมื่อ APK ตัวใหม่ถึงมือผู้ใช้ครบ

และ Android ชี้ `wbw.sumfu.store` (Node) ไม่ได้แตะ SUS เลย — deploy นี้จึงไม่กระทบ Android
