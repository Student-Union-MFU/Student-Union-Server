package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

/*
Critical 2 จากรีวิวรอบสุดท้าย: cursor ของฟีดเจ้าหน้าที่เดินทางกลับมาไม่รอด

ทำไมเทสนี้ต้องเดินผ่าน endpoint จริงและ Postgres จริง ไม่ใช่เรียก repo ตรงๆ ด้วย string ที่
เขียนเอง: ความพังทั้งหมดอยู่ "ระหว่าง" ชั้น ไม่ได้อยู่ในชั้นไหนชั้นเดียว —
  ฝั่ง Postgres    ปล่อย s.updated_at::text ออกมาเป็น "2026-08-06 16:56:44.807668+00"
  ฝั่ง iOS         ประกอบเป็น "<updated_at>|<id>" แล้ว escape ด้วย .urlQueryAllowed
                   ซึ่ง "ไม่" escape เครื่องหมาย + (escape แค่ช่องว่างกับ |)
  ฝั่ง Go          r.URL.Query().Get("since") ถอด + กลับเป็นช่องว่างตามกติกาของ query string
  ฝั่ง Postgres    ได้ "2026-08-06 16:56:44.807668 00|9" แล้วปฏิเสธทั้ง query

ผลคือ StaffFeed error → handler ตอบ 500 → StaffSOSStore.start กลืนด้วย try? และเพราะ cursor
ถูกเขียนใน apply() เท่านั้น (ซึ่งรันเฉพาะตอนสำเร็จ) cursor ที่มีพิษถูกส่งซ้ำตลอดกาล ตั้งแต่เคสจริง
เคสแรกของวันเป็นต้นไปฟีดตายถาวรโดยที่หน้าจอยังโชว์ชุดแรกอยู่และดูปกติดี

เทสนี้จึงเอา updated_at ที่ endpoint ส่งออกมาจริง มาประกอบเป็น cursor แบบเดียวกับที่แอปทำ
escape ด้วยกฎเดียวกับ Foundation แล้วยิงกลับเข้า endpoint เดิม
*/

// iosURLQueryAllowed — ชุดอักขระที่ Foundation.CharacterSet.urlQueryAllowed "ไม่" escape
//
// สังเกตว่ามี + อยู่ในชุดนี้ นั่นคือหัวใจของบั๊ก: + รอดออกไปดิบๆ แล้วถูกฝั่งรับถอดเป็นช่องว่าง
const iosURLQueryAllowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz" +
	"0123456789-._~!$&'()*+,;=:@/?"

// iosURLQueryEscape — เลียนแบบ addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed)
// ของ Swift ให้ตรงตัว จงใจไม่ใช้ url.QueryEscape ของ Go เพราะตัวนั้น escape + ให้ด้วย
// (เป็น %2B) ซึ่งจะปิดบังบั๊กที่กำลังจะทดสอบพอดี
func iosURLQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(iosURLQueryAllowed, s[i]) >= 0 {
			b.WriteByte(s[i])
			continue
		}
		fmt.Fprintf(&b, "%%%02X", s[i])
	}
	return b.String()
}

func openSOSCursorDB(t *testing.T) *pgxpool.Pool {
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
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("เปิดสวิตช์ WBW_DB_TESTS=1 ไว้แล้วแต่ต่อฐานข้อมูลไม่ได้ (%v)", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping ฐานข้อมูลไม่ผ่าน (%v) — ถ้าไม่มี .env ที่ root ให้ตั้ง WBW_TEST_DSN เอง", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedCursorParticipant — ผู้เข้าร่วมทดสอบหนึ่งคน (wbw_user + participant_profile) พร้อมล้างตอนจบ
func seedCursorParticipant(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO wbw_user (username, password_hash, role, display_name)
		VALUES ($1, 'x', 'participant', 'SOS Cursor Test')
		RETURNING user_id::text`,
		"sos-cursor-"+label+"-"+strconv.FormatInt(time.Now().UnixNano(), 36)).Scan(&id); err != nil {
		t.Fatalf("สร้างผู้เข้าร่วมทดสอบ (%s) ไม่สำเร็จ: %v", label, err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM sos_event WHERE participant_id = $1::uuid`, id); err != nil {
			t.Errorf("ล้างเคส SOS ทดสอบไม่สำเร็จ: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM wbw_user WHERE user_id = $1::uuid`, id); err != nil {
			t.Errorf("ล้างผู้เข้าร่วมทดสอบไม่สำเร็จ: %v", err)
		}
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO participant_profile (user_id) VALUES ($1::uuid)`, id); err != nil {
		t.Fatalf("สร้างโปรไฟล์ทดสอบ (%s) ไม่สำเร็จ: %v", label, err)
	}
	return id
}

// callStaffFeed — GET /wbw/staff/sos ผ่าน router จริงด้วย token บทบาท admin
//
// ใช้ admin ไม่ใช่ staff ธรรมดาโดยตั้งใจ: seesEverything คืน true ให้ admin ทันทีโดยไม่แตะ DB
// เทสจึงไม่ต้อง seed wbw_staff/checkpoint_staff เพิ่มเพื่อพิสูจน์เรื่องที่ไม่เกี่ยวกับ cursor เลย
// (การมองเห็นตามฐานมีเทสของตัวเองอยู่แล้วใน internal/repository)
func callStaffFeed(t *testing.T, h *WBWSOSHandler, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	router := newSOSTestRouter(t, h)
	req := httptest.NewRequest(http.MethodGet, "/wbw/staff/sos"+rawQuery, nil)
	req.Header.Set("Authorization", "Bearer "+
		signTestToken(t, "33333333-3333-3333-3333-333333333333", "admin"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeStaffFeed(t *testing.T, rec *httptest.ResponseRecorder) []model.SOSStaffCase {
	t.Helper()
	var list []model.SOSStaffCase
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("ตอบกลับไม่ใช่ JSON array ของเคส: %v (body=%s)", err, rec.Body)
	}
	return list
}

func feedHas(list []model.SOSStaffCase, id int64) bool {
	for _, c := range list {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestStaffFeedAcceptsTheCursorTheClientActuallyProduces — Critical 2
//
// เดินครบวง: เคสจริง → updated_at ที่ endpoint ส่งออกมา → cursor ที่แอปประกอบ → escape แบบ
// Foundation → กลับเข้า endpoint เดิม · ต้องได้ 200 พร้อมเคสที่ใหม่กว่า cursor และต้องไม่มีเคสที่
// cursor ชี้อยู่ (เทียบแบบ row-value เข้มงวด)
func TestStaffFeedAcceptsTheCursorTheClientActuallyProduces(t *testing.T) {
	pool := openSOSCursorDB(t)
	ctx := context.Background()

	pA := seedCursorParticipant(t, pool, "a")
	pB := seedCursorParticipant(t, pool, "b")

	repo := repository.NewWBWSOSRepository(pool)
	svc := service.NewWBWSOSService(repo, service.NewSOSEvents(nil, nil), nil, nil, "053-916-000")
	h := NewWBWSOSHandler(svc)

	caseA, _, err := svc.Raise(ctx, pA, model.SOSRequest{
		ClientID: "c0f50001-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("เปิดเคส A ไม่สำเร็จ: %v", err)
	}

	// รอบแรกไม่มี cursor — เอา updated_at ของเคส A ที่ "endpoint ส่งออกมาจริง" มาใช้ต่อ
	// ไม่ใช่ค่าที่เทสประกอบเอง นั่นคือประเด็นทั้งหมดของเทสนี้
	first := callStaffFeed(t, h, "?wait=0")
	if first.Code != http.StatusOK {
		t.Fatalf("รอบแรกต้องได้ 200 ได้ %d body=%s", first.Code, first.Body)
	}
	var rowA *model.SOSStaffCase
	for i, c := range decodeStaffFeed(t, first) {
		if c.ID == caseA.ID {
			rowA = &decodeStaffFeed(t, first)[i]
			break
		}
	}
	if rowA == nil {
		t.Fatalf("รอบแรกต้องมีเคส A (id=%d) อยู่ในฟีด", caseA.ID)
	}

	caseB, _, err := svc.Raise(ctx, pB, model.SOSRequest{
		ClientID: "c0f50001-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:00:05Z",
	})
	if err != nil {
		t.Fatalf("เปิดเคส B ไม่สำเร็จ: %v", err)
	}

	// ประกอบ cursor แบบเดียวกับ StaffSOSStore.apply แล้ว escape แบบเดียวกับ APIClient.staffSOSFeed
	cursor := fmt.Sprintf("%s|%d", rowA.UpdatedAt, rowA.ID)
	escaped := iosURLQueryEscape(cursor)

	second := callStaffFeed(t, h, "?wait=0&since="+escaped)
	if second.Code != http.StatusOK {
		t.Fatalf("cursor ที่แอปสร้างจากแถวจริงต้องใช้ได้ ได้ %d body=%s\n"+
			"  updated_at ที่ส่งออกมา = %q\n  cursor = %q\n  หลัง escape = %q\n"+
			"รูปแบบที่ Postgres ส่งออกมาต้องไม่มีทั้งช่องว่างและเครื่องหมาย + "+
			"ไม่งั้นมันเดินทางกลับผ่าน query string ไม่รอด",
			second.Code, second.Body, rowA.UpdatedAt, cursor, escaped)
	}

	list := decodeStaffFeed(t, second)
	if !feedHas(list, caseB.ID) {
		t.Fatalf("เคสที่ใหม่กว่า cursor (B id=%d) ต้องอยู่ในผล ได้ %d แถว", caseB.ID, len(list))
	}
	if feedHas(list, caseA.ID) {
		t.Fatalf("เคสที่ cursor ชี้อยู่ (A id=%d) ต้องไม่กลับมาซ้ำ", caseA.ID)
	}
}

// TestStaffFeedCursorSurvivesQueryStringDecoding — ยืนยันคุณสมบัติของ "รูปแบบ" ตรงๆ
//
// แยกจากเทสด้านบนเพื่อให้ข้อความล้มเหลวชี้ตรงจุดว่าอะไรผิด: ถ้ารูปแบบมีช่องว่างหรือ + อยู่
// ไม่ว่าฝั่งไหนจะ escape ดีแค่ไหนก็เป็นกับดักรอคนถัดไปอยู่ดี (การแก้แค่ฝั่ง iOS ไม่ปิดเรื่องนี้)
func TestStaffFeedCursorSurvivesQueryStringDecoding(t *testing.T) {
	pool := openSOSCursorDB(t)
	p := seedCursorParticipant(t, pool, "fmt")

	repo := repository.NewWBWSOSRepository(pool)
	svc := service.NewWBWSOSService(repo, service.NewSOSEvents(nil, nil), nil, nil, "053-916-000")
	h := NewWBWSOSHandler(svc)

	c, _, err := svc.Raise(context.Background(), p, model.SOSRequest{
		ClientID: "c0f50002-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := callStaffFeed(t, h, "?wait=0")
	var updatedAt string
	for _, row := range decodeStaffFeed(t, rec) {
		if row.ID == c.ID {
			updatedAt = row.UpdatedAt
		}
	}
	if updatedAt == "" {
		t.Fatalf("ไม่เจอเคส %d ในฟีด", c.ID)
	}
	if strings.ContainsAny(updatedAt, " +") {
		t.Fatalf("updated_at ที่ส่งออกมา (%q) มีช่องว่างหรือ + อยู่ — ทั้งสองตัวถูก query string "+
			"ตีความใหม่ตอนเดินทางกลับ (+ กลายเป็นช่องว่าง) cursor จึงพังทันทีที่ client ส่งกลับมา",
			updatedAt)
	}
}
