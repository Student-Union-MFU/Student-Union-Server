package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"su-server/internal/model"
)

/*
โควตาผู้เข้าร่วมทั้งงาน (migration 000021) — เทสของจริงบน Postgres เท่านั้น

สิ่งที่ต้องพิสูจน์คือ "เพดานทะลุไม่ได้แม้สมัครพร้อมกัน" ซึ่งเป็นพฤติกรรมของ row lock
กับ CHECK ในฐานข้อมูล ไม่ใช่ตรรกะใน Go — เอา pool ไปแทนด้วยของปลอมเมื่อไหร่ก็เท่ากับ
เลิกเทสสิ่งที่ตั้งใจจะเทส (ของปลอมไม่มี transaction ไม่มี lock ไม่มี constraint)

**ต้องเปิดเอง**: ตั้ง WBW_DB_TESTS=1 (สวิตช์เดียวกับเทส feedback)

ไม่ใช้ openTestDB ของเทส feedback เพราะตัวนั้นบังคับว่าต้องมีบัญชีทดสอบ 6931900011
กับแถว check_in ตั้งต้นอยู่ก่อน ซึ่งเทสชุดนี้ไม่ได้ใช้ — ต่อฐานข้อมูลเองด้วย DSN
แบบเดียวกันพอ

**เขียนอะไรบ้าง**: บัญชี participant ชั่วคราวที่ username ขึ้นต้นด้วย "captest-"
กับค่า max_participants ใน wbw_capacity ที่คืนค่าเดิมทุกครั้งทั้งตอนผ่านและตอนพัง

⚠ ห้ามใช้รหัสที่หน้าตาเหมือนรหัสนักศึกษาเป็นรหัสทดสอบ แล้วล้างด้วย LIKE '693%'
เพราะการล้างจะกวาดบัญชีจริงที่บังเอิญขึ้นต้นเหมือนกันไปด้วย (เคยเกิดมาแล้วตอนเขียนไฟล์นี้
— ลบบัญชีในฐาน dev ไปหนึ่งใบ) · username จริงเป็นตัวเลขล้วนเสมอ prefix ที่มีขีดกลาง
จึงชนของจริงไม่ได้เลย
*/

// openCapacityTestDB — ต่อฐานข้อมูลจริง ไม่ต้องมี fixture อะไรล่วงหน้า
func openCapacityTestDB(t *testing.T) *pgxpool.Pool {
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
		t.Fatalf("เปิดสวิตช์ WBW_DB_TESTS=1 ไว้แล้วแต่ ping ไม่ผ่าน (%v) — "+
			"ถ้าไม่มี .env ที่ root ให้ตั้ง WBW_TEST_DSN เอง", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testIDPrefix — ไม่ใช่ตัวเลขล้วน จึงเป็นไปไม่ได้ที่จะตรงกับ username ของคนจริง
const testIDPrefix = "captest-"

// minimalRegister — คำขอสมัครแบบน้อยที่สุดที่ผ่าน constraint ของตาราง
func minimalRegister(studentID string) model.RegisterRequest {
	var req model.RegisterRequest
	req.StudentID = studentID
	req.Profile.FirstName = "ทดสอบ"
	req.Profile.LastName = "โควตา"
	req.Profile.Sex = "unspecified"
	req.Consent.ConsentHealthData = true
	req.Consent.WaiverAccepted = true
	return req
}

// setMax ตั้งเพดานชั่วคราวแล้วคืนค่าเดิมให้อัตโนมัติเมื่อเทสจบ
func setMax(t *testing.T, r *WBWAuthRepository, seatsFree int) {
	t.Helper()
	ctx := context.Background()

	var oldMax int
	if err := r.db.QueryRow(ctx,
		`SELECT max_participants FROM wbw_capacity WHERE id`).Scan(&oldMax); err != nil {
		t.Fatalf("อ่าน max_participants ไม่ได้: %v", err)
	}
	if _, err := r.db.Exec(ctx,
		`UPDATE wbw_capacity SET max_participants = taken + $1 WHERE id`, seatsFree); err != nil {
		t.Fatalf("ตั้งเพดานชั่วคราวไม่ได้: %v", err)
	}
	t.Cleanup(func() {
		if _, err := r.db.Exec(context.Background(),
			`UPDATE wbw_capacity SET max_participants = $1 WHERE id`, oldMax); err != nil {
			t.Errorf("คืนค่า max_participants ไม่สำเร็จ (ค่าเดิม %d): %v", oldMax, err)
		}
	})
}

// cleanupTestUsers ลบบัญชีทดสอบทั้งหมด — trigger จะลด taken ให้เอง
func cleanupTestUsers(t *testing.T, r *WBWAuthRepository) {
	t.Helper()
	if _, err := r.db.Exec(context.Background(),
		`DELETE FROM wbw_user WHERE username LIKE $1`, testIDPrefix+"%"); err != nil {
		t.Errorf("ลบบัญชีทดสอบไม่สำเร็จ: %v", err)
	}
}

// TestCapacityBlocksOverflow — สมัครพร้อมกัน 25 คนบนที่นั่งว่าง 3 ที่ ต้องผ่าน 3 พอดี
//
// นี่คือกรณีที่การเช็คด้วย SELECT count(*) ใน Go พัง: ทุก goroutine อ่านเลขเดียวกัน
// ก่อนใครจะ commit แล้ว insert ทับกันหมด
func TestCapacityBlocksOverflow(t *testing.T) {
	pool := openCapacityTestDB(t)

	repo := NewWBWAuthRepository(pool)
	cleanupTestUsers(t, repo)
	t.Cleanup(func() { cleanupTestUsers(t, repo) })

	const seats, racers = 3, 25
	setMax(t, repo, seats)

	before, err := repo.Capacity(context.Background())
	if err != nil {
		t.Fatalf("อ่านโควตาไม่ได้: %v", err)
	}
	if before.SeatsLeft != seats {
		t.Fatalf("ตั้งต้นควรเหลือ %d ที่ ได้ %d", seats, before.SeatsLeft)
	}

	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // ปล่อยพร้อมกัน ให้ชนกันจริง ๆ
			sid := fmt.Sprintf("%s%03d", testIDPrefix, i)
			_, results[i] = repo.Register(
				context.Background(), minimalRegister(sid), sid, "$2a$10$notarealhashnotarealhash", nil)
		}(i)
	}
	close(start)
	wg.Wait()

	var ok, full, other int
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrFull):
			full++
		default:
			other++
			t.Errorf("error ที่ไม่คาดคิด: %v", err)
		}
	}
	if ok != seats {
		t.Errorf("ควรสมัครสำเร็จ %d คน ได้ %d (เต็ม %d · อื่น ๆ %d)", seats, ok, full, other)
	}
	if full != racers-seats {
		t.Errorf("ควรโดนปฏิเสธเพราะเต็ม %d คน ได้ %d", racers-seats, full)
	}

	after, err := repo.Capacity(context.Background())
	if err != nil {
		t.Fatalf("อ่านโควตาหลังเทสไม่ได้: %v", err)
	}
	if after.SeatsLeft != 0 || !after.Full {
		t.Errorf("หลังเต็มควรเหลือ 0 และ full=true ได้ seats_left=%d full=%v", after.SeatsLeft, after.Full)
	}
	if after.Taken != before.Taken+seats {
		t.Errorf("taken ควรเพิ่ม %d ได้ %d", seats, after.Taken-before.Taken)
	}
}

// TestCapacityRollsBackProfile — คนที่โดนปฏิเสธต้องไม่เหลือเศษข้อมูลไว้
// (Register ทำ 4 INSERT ใน transaction เดียว ถ้า CHECK พังต้องหายทั้งชุด)
func TestCapacityRollsBackProfile(t *testing.T) {
	pool := openCapacityTestDB(t)

	repo := NewWBWAuthRepository(pool)
	cleanupTestUsers(t, repo)
	t.Cleanup(func() { cleanupTestUsers(t, repo) })

	setMax(t, repo, 0) // ไม่เหลือที่นั่งเลย

	sid := testIDPrefix + "rollback"
	if _, err := repo.Register(
		context.Background(), minimalRegister(sid), sid, "$2a$10$notarealhashnotarealhash", nil,
	); !errors.Is(err, ErrFull) {
		t.Fatalf("ควรได้ ErrFull ได้: %v", err)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM wbw_user WHERE username = $1`, sid).Scan(&n); err != nil {
		t.Fatalf("นับแถวไม่ได้: %v", err)
	}
	if n != 0 {
		t.Errorf("ต้องไม่มีแถว wbw_user ค้างไว้ เจอ %d แถว", n)
	}
}

// TestCapacityIgnoresStaffAndAdmin — โควตานับเฉพาะ participant
func TestCapacityIgnoresStaffAndAdmin(t *testing.T) {
	pool := openCapacityTestDB(t)

	repo := NewWBWAuthRepository(pool)
	cleanupTestUsers(t, repo)
	t.Cleanup(func() { cleanupTestUsers(t, repo) })

	setMax(t, repo, 0) // เต็มสำหรับผู้เข้าร่วม — staff ต้องยังสมัครได้

	before, err := repo.Capacity(context.Background())
	if err != nil {
		t.Fatalf("อ่านโควตาไม่ได้: %v", err)
	}

	sid := testIDPrefix + "staff"
	schoolID := 1
	if _, err := repo.RegisterStaff(
		context.Background(), sid, "$2a$10$notarealhashnotarealhash", schoolID, nil, "registration",
	); err != nil {
		t.Fatalf("staff ต้องสมัครได้แม้ที่นั่งผู้เข้าร่วมเต็ม: %v", err)
	}

	after, err := repo.Capacity(context.Background())
	if err != nil {
		t.Fatalf("อ่านโควตาหลังสมัคร staff ไม่ได้: %v", err)
	}
	if after.Taken != before.Taken {
		t.Errorf("taken ต้องไม่ขยับตอน staff สมัคร (%d → %d)", before.Taken, after.Taken)
	}
}
