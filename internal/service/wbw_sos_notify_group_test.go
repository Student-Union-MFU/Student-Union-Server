package service

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

/*
เทสชุดนี้ปิดช่องที่รีวิวรอบสุดท้ายเจอ: แถวแจ้งเตือนของกลุ่มไม่เคยถูกสร้างจริงเลย

ทำไมต้องแตะ Postgres จริง ไม่ใช่ของปลอม: ทั้งสองข้อบกพร่องอยู่ในค่าที่ "ฐานข้อมูล" เท่านั้นที่
ปฏิเสธหรือกรองทิ้ง — level "urgent" ไม่ใช่สมาชิกของ enum noti_level ('info','warning','emergency')
INSERT จึงพังที่ Postgres ไม่ใช่ที่ Go · และ audience_id ที่เป็น NULL ทำให้เงื่อนไข
`n.audience = 'group' AND n.audience_id = p.group_id::text` ใน ListForUser ไม่เป็นจริงกับใครเลย
(NULL = '3' ไม่ใช่ TRUE) ของปลอมที่แค่จำว่า Create ถูกเรียกด้วยอะไรผ่านฉลุยทั้งสองกรณี — เทสแบบนั้น
เขียวสนิทกับโค้ดที่พังอยู่แล้ว (fakeSOSNoti ใน wbw_sos_fanout_test.go เป็นแบบนั้นจริงๆ) จึงต้องยืนยัน
ที่ปลายทางที่คนใช้จริงเห็น: แถวใน notification และผลของ ListForUser ของเพื่อนร่วมกลุ่ม
*/

func openSOSNotifyDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("WBW_DB_TESTS") != "1" {
		t.Skip("ข้าม: ต้องมี Postgres จริง — ตั้ง WBW_DB_TESTS=1 เพื่อเปิด")
	}
	_ = godotenv.Load("../../.env")

	dsn := os.Getenv("WBW_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("DB_USER") + ":" + os.Getenv("DB_PASS") +
			"@" + os.Getenv("DB_HOST") + ":" + os.Getenv("DB_PORT") + "/" + os.Getenv("DB_NAME")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("เปิดสวิตช์ WBW_DB_TESTS=1 ไว้แล้วแต่ต่อฐานข้อมูลไม่ได้ (%v)", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("เปิดสวิตช์ WBW_DB_TESTS=1 ไว้แล้วแต่ ping ฐานข้อมูลไม่ผ่าน (%v) — "+
			"ถ้าไม่มี .env ที่ root ให้ตั้ง WBW_TEST_DSN เอง", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// sosNotifyFixture — กลุ่มหนึ่งกลุ่ม คนกดหนึ่งคน เพื่อนร่วมกลุ่มหนึ่งคน และคนนอกกลุ่มหนึ่งคน
//
// ลำดับ t.Cleanup เป็น LIFO จึงลงทะเบียนย้อนกลับจากลำดับที่อยากให้ลบจริง: กลุ่มก่อน (รันท้ายสุด)
// แล้วผู้ใช้ แล้ว notification/sos_event (รันแรกสุด) — notification.created_by อ้าง wbw_user
// แบบไม่มี ON DELETE CASCADE ถ้าลบผู้ใช้ก่อนจะติด FK
type sosNotifyFixture struct {
	pool      *pgxpool.Pool
	groupID   int
	presser   string
	groupmate string
	outsider  string
}

func newSOSNotifyFixture(t *testing.T) *sosNotifyFixture {
	t.Helper()
	pool := openSOSNotifyDB(t)
	ctx := context.Background()

	var groupID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO participant_group (group_number)
		SELECT COALESCE(MAX(group_number), 0) + 1 FROM participant_group
		RETURNING group_id`).Scan(&groupID); err != nil {
		t.Fatalf("สร้างกลุ่มทดสอบไม่สำเร็จ: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM participant_group WHERE group_id = $1`, groupID); err != nil {
			t.Errorf("ล้างกลุ่มทดสอบไม่สำเร็จ: %v", err)
		}
	})

	users := []string{}
	newUser := func(label string, group *int) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO wbw_user (username, password_hash, role, display_name)
			VALUES ($1, 'x', 'participant', 'SOS Notify Test')
			RETURNING user_id::text`,
			"sos-noti-"+label+"-"+strconv.FormatInt(time.Now().UnixNano(), 36)).Scan(&id); err != nil {
			t.Fatalf("สร้างผู้ใช้ทดสอบ (%s) ไม่สำเร็จ: %v", label, err)
		}
		users = append(users, id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO participant_profile (user_id, group_id) VALUES ($1::uuid, $2)`,
			id, group); err != nil {
			t.Fatalf("สร้างโปรไฟล์ทดสอบ (%s) ไม่สำเร็จ: %v", label, err)
		}
		return id
	}

	f := &sosNotifyFixture{pool: pool, groupID: groupID}
	// ลงทะเบียนล้างผู้ใช้ก่อนสร้างจริง — ถ้า insert คนที่สองพัง คนแรกต้องไม่ค้าง (ทรงเดียวกับ
	// newSOSTestRepo ฝั่ง repository ที่เคยโดนบั๊กนี้มาแล้ว)
	t.Cleanup(func() {
		for _, id := range users {
			if _, err := pool.Exec(context.Background(),
				`DELETE FROM wbw_user WHERE user_id = $1::uuid`, id); err != nil {
				t.Errorf("ล้างผู้ใช้ทดสอบไม่สำเร็จ: %v", err)
			}
		}
	})

	f.presser = newUser("presser", &groupID)
	f.groupmate = newUser("mate", &groupID)
	f.outsider = newUser("outsider", nil)

	t.Cleanup(func() {
		cctx := context.Background()
		for _, id := range users {
			if _, err := pool.Exec(cctx,
				`DELETE FROM notification WHERE created_by = $1::uuid`, id); err != nil {
				t.Errorf("ล้างแถวแจ้งเตือนทดสอบไม่สำเร็จ: %v", err)
			}
			if _, err := pool.Exec(cctx,
				`DELETE FROM sos_event WHERE participant_id = $1::uuid`, id); err != nil {
				t.Errorf("ล้างเคส SOS ทดสอบไม่สำเร็จ: %v", err)
			}
		}
		if _, err := pool.Exec(cctx,
			`DELETE FROM notification WHERE audience = 'group' AND audience_id = $1`,
			strconv.Itoa(groupID)); err != nil {
			t.Errorf("ล้างแถวแจ้งเตือนของกลุ่มทดสอบไม่สำเร็จ: %v", err)
		}
	})
	return f
}

// waitForGroupNoti — รอ goroutine ของ announce (fire-and-forget) เขียนแถวเสร็จ
//
// ไม่รอ push แทน เพราะสิ่งที่ต้องพิสูจน์คือ "แถวเกิดจริง" ไม่ใช่ "โค้ดเดินผ่านบรรทัดนั้น" —
// ของเดิม Create คืน error แล้วถูก slog.Error กลืน push ก็ยิงต่อตามปกติ การรอ push จึงผ่านได้
// ทั้งที่ไม่มีแถวไหนเกิดขึ้นเลย
func waitForGroupNoti(t *testing.T, pool *pgxpool.Pool, caseID int64) (level, audience string, audienceID *string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	ref := strconv.FormatInt(caseID, 10)
	for {
		err := pool.QueryRow(context.Background(), `
			SELECT level::text, audience::text, audience_id
			  FROM notification
			 WHERE type = 'sos' AND ref_id = $1
			 ORDER BY id DESC LIMIT 1`, ref).Scan(&level, &audience, &audienceID)
		if err == nil {
			return level, audience, audienceID
		}
		if time.Now().After(deadline) {
			t.Fatalf("ไม่มีแถวแจ้งเตือนของกลุ่มสำหรับเคส %d เลยภายใน 5 วิ (ล่าสุด: %v) — "+
				"คน 50 คนในกลุ่มไม่มีทางเห็นอะไรในแอปเลย", caseID, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRaiseNotifyGroupWritesARowTheGroupCanActuallySee — Critical 1 จากรีวิวรอบสุดท้าย
//
// สองข้อบกพร่องแยกกันบนการเรียกเดียวกัน ทั้งคู่จบลงที่ "กลุ่มไม่เห็นอะไรเลย":
//  1. level = "urgent" ไม่ใช่สมาชิกของ enum noti_level → INSERT พังทั้งแถว
//  2. AudienceID ไม่เคยถูกตั้ง → ต่อให้ level ถูก แถวก็มองไม่เห็นจาก ListForUser ของใครเลย
func TestRaiseNotifyGroupWritesARowTheGroupCanActuallySee(t *testing.T) {
	f := newSOSNotifyFixture(t)
	ctx := context.Background()

	sosRepo := repository.NewWBWSOSRepository(f.pool)
	notiRepo := repository.NewWBWNotificationRepository(f.pool)
	notiSvc := NewWBWNotificationService(notiRepo)
	push := newFakeSOSPush()

	svc := NewWBWSOSService(sosRepo, NewSOSEvents(nil, nil), push, notiSvc, "053-916-000")
	c, created, err := svc.Raise(ctx, f.presser, model.SOSRequest{
		ClientID:   "d0d0d0d0-0000-0000-0000-00000000f001",
		DeviceTime: "2026-08-06T10:00:00Z",
	})
	if err != nil || !created {
		t.Fatalf("เปิดเคสไม่สำเร็จ: created=%v err=%v", created, err)
	}

	level, audience, audienceID := waitForGroupNoti(t, f.pool, c.ID)
	if level != "emergency" {
		t.Fatalf("level ต้องเป็น emergency (สมาชิกของ enum noti_level) ได้ %q", level)
	}
	if audience != "group" {
		t.Fatalf("audience ต้องเป็น group ได้ %q", audience)
	}
	if audienceID == nil {
		t.Fatal("audience_id ต้องไม่เป็น NULL — ListForUser เทียบ n.audience_id = p.group_id::text " +
			"ซึ่ง NULL ไม่มีวันเท่ากับอะไรเลย แถวจะมองไม่เห็นจากทุกคน")
	}
	if *audienceID != strconv.Itoa(f.groupID) {
		t.Fatalf("audience_id ต้องเป็นกลุ่มของคนกด (%d) ได้ %q", f.groupID, *audienceID)
	}

	// ปลายทางจริงที่แอปเรียก — ไม่ใช่แค่รูปร่างของแถวในตาราง
	mate, err := notiRepo.ListForUser(ctx, f.groupmate)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSOSNoti(mate, c.ID) {
		t.Fatalf("เพื่อนร่วมกลุ่มต้องเห็นแถวนี้ใน ListForUser (ได้ %d แถว ไม่มีเคส %d)", len(mate), c.ID)
	}

	// และต้องไม่รั่วไปหาคนนอกกลุ่ม
	out, err := notiRepo.ListForUser(ctx, f.outsider)
	if err != nil {
		t.Fatal(err)
	}
	if containsSOSNoti(out, c.ID) {
		t.Fatal("คนนอกกลุ่มต้องไม่เห็นเคสนี้")
	}
}

func containsSOSNoti(list []model.Notification, caseID int64) bool {
	ref := strconv.FormatInt(caseID, 10)
	for _, n := range list {
		if n.RefID != nil && *n.RefID == ref && n.Type == "sos" {
			return true
		}
	}
	return false
}
