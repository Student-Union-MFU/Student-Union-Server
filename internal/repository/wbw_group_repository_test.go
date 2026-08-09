package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

/*
เทสชุดนี้ต้องมี Postgres จริง — ปลอมไม่ได้

สิ่งที่เทสคือพฤติกรรมของ SQL ล้วน ๆ: การหักโควตาต้องเกิดใน UPDATE statement เดียวกับที่เคลียร์
group_id (ไม่งั้นสองคำขอพร้อมกันหักเกินได้) และ FK/constraint ของ group_membership_log
ของปลอมไม่มีทางรู้เรื่องเหล่านี้ เทสกับของปลอมจึงเท่ากับไม่ได้เทสอะไรเลย

เปิดด้วย WBW_DB_TESTS=1 เท่านั้น · เขียนทับ group_id/leave_quota ของบัญชีทดสอบ 6931900011
แล้วคืนค่าเดิมให้ครบทุกครั้งใน Cleanup
*/

func openGroupTestDB(t *testing.T) (*pgxpool.Pool, string) {
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
		t.Fatalf("เปิดสวิตช์ WBW_DB_TESTS=1 ไว้แล้วแต่ ping ไม่ผ่าน (%v)", err)
	}
	t.Cleanup(pool.Close) // ลงทะเบียนก่อน = ทำงานทีหลังสุด (Cleanup เป็น LIFO) คืนค่าเสร็จค่อยปิด pool

	var uid string
	if err := pool.QueryRow(ctx,
		`SELECT user_id::text FROM wbw_user WHERE username = $1`, testUsername).Scan(&uid); err != nil {
		t.Fatalf("ไม่มีบัญชีทดสอบ %s ในฐานข้อมูลนี้ (%v)", testUsername, err)
	}

	var prevGroup *int
	var prevQuota int
	if err := pool.QueryRow(ctx,
		`SELECT group_id, leave_quota FROM participant_profile WHERE user_id = $1`, uid,
	).Scan(&prevGroup, &prevQuota); err != nil {
		t.Fatalf("อ่านสถานะเดิมของบัญชีทดสอบไม่ได้ (%v)", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx,
			`UPDATE participant_profile SET group_id = $2, leave_quota = $3 WHERE user_id = $1`,
			uid, prevGroup, prevQuota); err != nil {
			t.Errorf("คืนสถานะเดิมของบัญชีทดสอบไม่สำเร็จ (%v)", err)
		}
		pool.Exec(ctx, `DELETE FROM group_membership_log WHERE user_id = $1`, uid)
		pool.Exec(ctx, `DELETE FROM group_chat_state WHERE user_id = $1`, uid)
	})

	return pool, uid
}

// setMembership — ตั้งสถานะตั้งต้นของบัญชีทดสอบ แล้วล้าง log เก่าทิ้ง ให้แต่ละเทสนับแถวได้ตรง ๆ
func setMembership(t *testing.T, pool *pgxpool.Pool, uid string, groupID *int, quota int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`UPDATE participant_profile SET group_id = $2, leave_quota = $3 WHERE user_id = $1`,
		uid, groupID, quota); err != nil {
		t.Fatalf("ตั้งสถานะตั้งต้นไม่สำเร็จ (%v)", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM group_membership_log WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("ล้าง log เก่าไม่สำเร็จ (%v)", err)
	}
}

// freeGroupID — กลุ่มที่ยังมีที่นั่งว่าง ใช้เป็นปลายทางของ join ในเทส
func freeGroupID(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var gid int
	if err := pool.QueryRow(context.Background(),
		`SELECT group_id FROM participant_group WHERE member_count < capacity ORDER BY group_id LIMIT 1`,
	).Scan(&gid); err != nil {
		t.Fatalf("ไม่มีกลุ่มว่างให้ทดสอบ (%v)", err)
	}
	return gid
}

func membershipLogCount(t *testing.T, pool *pgxpool.Pool, uid, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM group_membership_log WHERE user_id = $1 AND action = $2`,
		uid, action).Scan(&n); err != nil {
		t.Fatalf("นับ log ไม่สำเร็จ (%v)", err)
	}
	return n
}

func readMembership(t *testing.T, pool *pgxpool.Pool, uid string) (*int, int) {
	t.Helper()
	var gid *int
	var quota int
	if err := pool.QueryRow(context.Background(),
		`SELECT group_id, leave_quota FROM participant_profile WHERE user_id = $1`, uid,
	).Scan(&gid, &quota); err != nil {
		t.Fatalf("อ่านสถานะไม่สำเร็จ (%v)", err)
	}
	return gid, quota
}

func TestLeaveWithoutQuotaIsRejected(t *testing.T) {
	pool, uid := openGroupTestDB(t)
	repo := NewWBWGroupRepository(pool)
	gid := freeGroupID(t, pool)
	setMembership(t, pool, uid, &gid, 0)

	_, err := repo.Leave(context.Background(), uid)
	if !errors.Is(err, ErrNoQuota) {
		t.Fatalf("อยากได้ ErrNoQuota แต่ได้ %v", err)
	}
	got, quota := readMembership(t, pool, uid)
	if got == nil || *got != gid {
		t.Errorf("ต้องยังอยู่กลุ่มเดิม %d แต่ได้ %v", gid, got)
	}
	if quota != 0 {
		t.Errorf("โควตาต้องคงที่ 0 แต่ได้ %d", quota)
	}
	if n := membershipLogCount(t, pool, uid, "leave"); n != 0 {
		t.Errorf("ห้ามมี log leave ตอนออกไม่สำเร็จ แต่มี %d แถว", n)
	}
}

func TestLeaveSpendsQuotaAndLogs(t *testing.T) {
	pool, uid := openGroupTestDB(t)
	repo := NewWBWGroupRepository(pool)
	gid := freeGroupID(t, pool)
	setMembership(t, pool, uid, &gid, 1)

	prev, err := repo.Leave(context.Background(), uid)
	if err != nil {
		t.Fatalf("ออกจากกลุ่มไม่สำเร็จ (%v)", err)
	}
	if prev != gid {
		t.Errorf("ต้องคืนกลุ่มเดิม %d แต่ได้ %d", gid, prev)
	}
	got, quota := readMembership(t, pool, uid)
	if got != nil {
		t.Errorf("ต้องไม่มีกลุ่มแล้ว แต่ยังอยู่กลุ่ม %d", *got)
	}
	if quota != 0 {
		t.Errorf("โควตาต้องเหลือ 0 แต่ได้ %d", quota)
	}

	var loggedGroup int
	var quotaAfter int
	if err := pool.QueryRow(context.Background(),
		`SELECT group_id, quota_after FROM group_membership_log
		  WHERE user_id = $1 AND action = 'leave' ORDER BY log_id DESC LIMIT 1`, uid,
	).Scan(&loggedGroup, &quotaAfter); err != nil {
		t.Fatalf("ไม่มีแถว log leave (%v)", err)
	}
	if loggedGroup != gid || quotaAfter != 0 {
		t.Errorf("log ต้องเป็น (กลุ่ม %d, quota_after 0) แต่ได้ (%d, %d)", gid, loggedGroup, quotaAfter)
	}
}

func TestLeaveWithoutGroupKeepsQuota(t *testing.T) {
	pool, uid := openGroupTestDB(t)
	repo := NewWBWGroupRepository(pool)
	setMembership(t, pool, uid, nil, 1)

	_, err := repo.Leave(context.Background(), uid)
	if !errors.Is(err, ErrNoGroup) {
		t.Fatalf("อยากได้ ErrNoGroup แต่ได้ %v", err)
	}
	if _, quota := readMembership(t, pool, uid); quota != 1 {
		t.Errorf("โควตาต้องไม่ถูกแตะ (1) แต่ได้ %d", quota)
	}
}

func TestLeaveConcurrentSpendsQuotaOnce(t *testing.T) {
	pool, uid := openGroupTestDB(t)
	repo := NewWBWGroupRepository(pool)
	gid := freeGroupID(t, pool)
	setMembership(t, pool, uid, &gid, 1)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.Leave(context.Background(), uid)
		}(i)
	}
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrNoQuota) && !errors.Is(err, ErrNoGroup) {
			t.Errorf("ตัวที่ล้มเหลวต้องเป็น ErrNoQuota/ErrNoGroup แต่ได้ %v", err)
		}
	}
	if success != 1 {
		t.Errorf("ต้องสำเร็จแค่ 1 จาก 2 แต่สำเร็จ %d", success)
	}
	if _, quota := readMembership(t, pool, uid); quota != 0 {
		t.Errorf("โควตาห้ามติดลบหรือหักเกิน — ต้องเป็น 0 แต่ได้ %d", quota)
	}
	if n := membershipLogCount(t, pool, uid, "leave"); n != 1 {
		t.Errorf("ต้องมี log leave แถวเดียว แต่มี %d", n)
	}
}

func TestJoinWhileInGroupIsRejected(t *testing.T) {
	pool, uid := openGroupTestDB(t)
	repo := NewWBWGroupRepository(pool)
	gid := freeGroupID(t, pool)
	setMembership(t, pool, uid, &gid, 1)

	// เป้าหมายคนละกลุ่มกับที่อยู่ — ถ้าไม่ปิดช่องนี้ จะย้ายกลุ่มได้โดยไม่เสียโควตาเลย
	var other int
	if err := pool.QueryRow(context.Background(),
		`SELECT group_id FROM participant_group
		  WHERE member_count < capacity AND group_id <> $1 ORDER BY group_id LIMIT 1`, gid,
	).Scan(&other); err != nil {
		t.Fatalf("ต้องมีกลุ่มว่างอีกกลุ่มเพื่อทดสอบ (%v)", err)
	}

	err := repo.Join(context.Background(), uid, other)
	if !errors.Is(err, ErrAlreadyInGroup) {
		t.Fatalf("อยากได้ ErrAlreadyInGroup แต่ได้ %v", err)
	}
	got, _ := readMembership(t, pool, uid)
	if got == nil || *got != gid {
		t.Errorf("ต้องยังอยู่กลุ่มเดิม %d แต่ได้ %v", gid, got)
	}
	if n := membershipLogCount(t, pool, uid, "join"); n != 0 {
		t.Errorf("ห้ามมี log join ตอนถูกปฏิเสธ แต่มี %d แถว", n)
	}
}

func TestJoinLogsWithoutSpendingQuota(t *testing.T) {
	pool, uid := openGroupTestDB(t)
	repo := NewWBWGroupRepository(pool)
	setMembership(t, pool, uid, nil, 1)
	gid := freeGroupID(t, pool)

	if err := repo.Join(context.Background(), uid, gid); err != nil {
		t.Fatalf("เข้ากลุ่มไม่สำเร็จ (%v)", err)
	}
	got, quota := readMembership(t, pool, uid)
	if got == nil || *got != gid {
		t.Errorf("ต้องอยู่กลุ่ม %d แต่ได้ %v", gid, got)
	}
	if quota != 1 {
		t.Errorf("การเข้ากลุ่มห้ามหักโควตา — ต้องเหลือ 1 แต่ได้ %d", quota)
	}

	var loggedGroup, quotaAfter int
	if err := pool.QueryRow(context.Background(),
		`SELECT group_id, quota_after FROM group_membership_log
		  WHERE user_id = $1 AND action = 'join' ORDER BY log_id DESC LIMIT 1`, uid,
	).Scan(&loggedGroup, &quotaAfter); err != nil {
		t.Fatalf("ไม่มีแถว log join (%v)", err)
	}
	if loggedGroup != gid || quotaAfter != 1 {
		t.Errorf("log ต้องเป็น (กลุ่ม %d, quota_after 1) แต่ได้ (%d, %d)", gid, loggedGroup, quotaAfter)
	}
}
