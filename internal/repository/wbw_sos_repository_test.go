package repository

import (
	"context"
	"os"
	"testing"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// เทสชุดนี้ต้องมี Postgres จริง — พฤติกรรมที่ทดสอบคือ SQL ล้วน (partial unique index,
// FOR UPDATE, การแยก 23505) ปลอมไม่ได้ · เปิดด้วย WBW_DB_TESTS=1 เหมือน feedback
func skipWithoutDB(t *testing.T) {
	t.Helper()
	if os.Getenv("WBW_DB_TESTS") != "1" {
		t.Skip("ต้องมี Postgres — ตั้ง WBW_DB_TESTS=1 เพื่อรัน")
	}
}

func TestRaiseCreatesThenRepeatOfSameClientIDUpdatesInPlace(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)

	lat, lng, acc := 20.04390, 99.89900, 12.0
	first, created, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "aaaaaaaa-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil || !created {
		t.Fatalf("เคสแรกต้องถูกสร้าง: created=%v err=%v", created, err)
	}

	second, created2, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "aaaaaaaa-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:08Z",
		Lat: &lat, Lng: &lng, AccuracyM: &acc,
	}, intPtr(2), strPtr("gps"))
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("client_id เดิมต้องไม่สร้างแถวใหม่")
	}
	if second.ID != first.ID {
		t.Fatalf("ต้องเป็นแถวเดิม: %d vs %d", first.ID, second.ID)
	}
	if second.CheckpointID == nil || *second.CheckpointID != 2 {
		t.Fatal("พิกัดรอบสองต้องอัปเดตฐานให้ด้วย")
	}
}

func TestRaiseWithNewClientIDWhileOpenIsABumpNotASecondCase(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)

	first, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "bbbbbbbb-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "bbbbbbbb-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:02:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("กดซ้ำระหว่างเคสเปิดต้องเป็นการย้ำแถวเดิม ได้ created=%v id=%d", created, second.ID)
	}
}

func TestRaiseKeepsForOtherTrueOnceAnyPressSaysSo(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)

	if _, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "cccccccc-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
		ForOther: true,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "cccccccc-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:01:00Z",
		ForOther: false,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ForOther {
		t.Fatal("for_other ต้องเป็น OR ไม่ใช่การเขียนทับ — มีคนเจ็บอยู่แล้ว")
	}
}

func TestCancelBeforeAckWorksAndAfterAckIsRejected(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)
	staff := seedStaffUser(t)

	c, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "dddddddd-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Ack(ctx, c.ID, staff); err != nil {
		t.Fatal(err)
	}
	if err := repo.Cancel(ctx, participant, c.ID); err != ErrSOSAlreadyAcked {
		t.Fatalf("ยกเลิกหลังรับเรื่องต้องได้ ErrSOSAlreadyAcked ได้ %v", err)
	}
}

func TestResolveClosesTheCaseAndFreesTheOneOpenSlot(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)
	staff := seedStaffUser(t)

	c, _, _ := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "eeeeeeee-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err := repo.Resolve(ctx, c.ID, staff, "helped"); err != nil {
		t.Fatal(err)
	}
	next, created, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "eeeeeeee-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T11:00:00Z",
	}, nil, nil)
	if err != nil || !created || next.ID == c.ID {
		t.Fatalf("ปิดเคสแล้วต้องเปิดเคสใหม่ได้: created=%v err=%v", created, err)
	}
}

/*
newSOSTestRepo / seedStaffUser — ทรงเดียวกับ openTestDB ใน wbw_feedback_repository_test.go:
เปิด pool จาก WBW_TEST_DSN (fallback ไป .env ที่ root แบบเดียวกัน) แล้วปิดเองตอนจบเทส

ต่างจาก feedback ตรงที่ฟีเจอร์นี้ไม่มีบัญชีทดสอบตายตัวบนฐานข้อมูล — ทุกเทสสร้าง wbw_user +
participant_profile ของตัวเองใหม่แบบสุ่ม (username จาก newUUID() ที่นิยามไว้แล้วในไฟล์
wbw_feedback_repository_test.go) แล้วลบทิ้งทั้งหมดตอนจบ เพราะ sos_event.participant_id
เป็น FK ไป wbw_user ตรงๆ — เคสจะอ้างอิงคนที่ไม่มีจริงไม่ได้

client_id ที่แต่ละเทสใช้เป็นค่าคงที่ตามที่โจทย์กำหนด (ไม่ใช่ของสุ่มแบบผู้ใช้) เพราะ Step 1
ต้องพิสูจน์พฤติกรรม "client_id เดิม = ย้ำ" ด้วยค่าที่รู้ล่วงหน้า คอลัมน์ client_id unique
ทั้งตาราง ไม่ผูกกับผู้เข้าร่วมคนใดคนหนึ่ง จึงต้องล้างค่าคงที่พวกนี้ทิ้งก่อนเริ่มทุกเทสด้วย
กันรันก่อนหน้าตายกลางคัน (cleanup ปกติไม่ได้รัน) ทิ้งแถวค้างที่ชนกับรันนี้
*/

// testSOSClientIDs — client_id ทุกตัวที่เทสในไฟล์นี้ใช้ตรงๆ
var testSOSClientIDs = []string{
	"aaaaaaaa-0000-0000-0000-000000000001",
	"bbbbbbbb-0000-0000-0000-000000000001", "bbbbbbbb-0000-0000-0000-000000000002",
	"cccccccc-0000-0000-0000-000000000001", "cccccccc-0000-0000-0000-000000000002",
	"dddddddd-0000-0000-0000-000000000001",
	"eeeeeeee-0000-0000-0000-000000000001", "eeeeeeee-0000-0000-0000-000000000002",
}

// openSOSTestDB — เปิด pool ทดสอบหนึ่งตัว แล้วล้างเคสทดสอบเก่า (ถ้ามีค้างจากรันก่อนหน้า
// ที่ตายกลางคัน) ก่อนเทสเริ่มจริง
func openSOSTestDB(t *testing.T) *pgxpool.Pool {
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

	if _, err := pool.Exec(ctx,
		`DELETE FROM sos_event WHERE client_id::text = ANY($1)`, testSOSClientIDs); err != nil {
		t.Fatalf("ล้างเคสทดสอบเก่าจากรันก่อนหน้าไม่สำเร็จ: %v", err)
	}

	return pool
}

// newSOSTestRepo — repo ทดสอบ + ผู้เข้าร่วมทดสอบใหม่หนึ่งคน (wbw_user + participant_profile)
// ลบแถวทั้งหมดที่สร้าง (เคส SOS ก่อน แล้วค่อยตัวผู้ใช้) ตอนจบเทสเสมอ ไม่ว่าเทสจะผ่านหรือ Fatal
func newSOSTestRepo(t *testing.T) (*WBWSOSRepository, string) {
	t.Helper()
	pool := openSOSTestDB(t)
	ctx := context.Background()

	username := "sos-participant-" + newUUID()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO wbw_user (username, password_hash, role, display_name)
		VALUES ($1, 'x', 'participant', 'SOS Test Participant')
		RETURNING user_id::text`, username).Scan(&userID); err != nil {
		t.Fatalf("สร้างผู้เข้าร่วมทดสอบไม่สำเร็จ: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO participant_profile (user_id) VALUES ($1::uuid)`, userID); err != nil {
		t.Fatalf("สร้างโปรไฟล์ผู้เข้าร่วมทดสอบไม่สำเร็จ: %v", err)
	}

	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx,
			`DELETE FROM sos_event WHERE participant_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างเคส SOS ทดสอบไม่สำเร็จ: %v", err)
		}
		// participant_profile หายเองด้วย ON DELETE CASCADE จาก wbw_user — ไม่ต้องลบแยก
		if _, err := pool.Exec(cctx,
			`DELETE FROM wbw_user WHERE user_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างผู้เข้าร่วมทดสอบไม่สำเร็จ: %v", err)
		}
	})

	return NewWBWSOSRepository(pool), userID
}

// seedStaffUser — บัญชีเจ้าหน้าที่ขั้นต่ำสำหรับ Ack/Resolve · ลบทิ้งตอนจบเทสเสมอ
//
// ล้าง sos_event ที่ acked_by/resolved_by ชี้มาที่บัญชีนี้ก่อนลบตัว wbw_user เอง —
// เขียนแยกจาก newSOSTestRepo (ไม่พึ่งให้อีกฝั่งล้างให้ก่อน) เพราะลำดับ t.Cleanup เป็น LIFO
// ขึ้นกับว่าเทสเรียก newSOSTestRepo หรือ seedStaffUser ก่อนกัน จะสมมติลำดับตายตัวไม่ได้
func seedStaffUser(t *testing.T) string {
	t.Helper()
	pool := openSOSTestDB(t)
	ctx := context.Background()

	username := "sos-staff-" + newUUID()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO wbw_user (username, password_hash, role, display_name)
		VALUES ($1, 'x', 'staff', 'SOS Test Staff')
		RETURNING user_id::text`, username).Scan(&userID); err != nil {
		t.Fatalf("สร้างเจ้าหน้าที่ทดสอบไม่สำเร็จ: %v", err)
	}

	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx,
			`DELETE FROM sos_event WHERE acked_by = $1::uuid OR resolved_by = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างเคส SOS ที่ผูกกับเจ้าหน้าที่ทดสอบไม่สำเร็จ: %v", err)
		}
		if _, err := pool.Exec(cctx,
			`DELETE FROM wbw_user WHERE user_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างเจ้าหน้าที่ทดสอบไม่สำเร็จ: %v", err)
		}
	})

	return userID
}

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }
