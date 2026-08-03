package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"su-server/internal/model"
)

// newUUID — uuid v4 แบบสั้นๆ ในเทส · ไม่ดึง module ใหม่เข้ามาเพื่อฟังก์ชันเดียว
// (client_id/participant_id เป็นคอลัมน์ uuid สตริงมั่วๆ จึงใช้ไม่ได้)
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

/*
เทสของ repository ต้องมี Postgres จริง — ปลอมไม่ได้

สามอย่างที่เทสในไฟล์นี้เป็นพฤติกรรมของ SQL ล้วนๆ ไม่ใช่ตรรกะใน Go:
  - แยก 23505 สองสาเหตุ (ชนที่ client_id ตัวเอง = retry คืน 200 · ชนที่
    uniq_feedback_participant_checkpoint = ตอบไปแล้วจริง คืน 409)
  - ErrNotCheckedIn
  - ตัวกรอง requires_checkin (ฐานบริการห้ามรับความเห็นแม้จะมีแถว check_in อยู่)

ทั้งสามข้อขึ้นกับ constraint จริงบนตาราง การเอา pool ไปแทนด้วยของปลอมจะกลายเป็นการเทส
ของปลอมทันที (ของปลอมไม่มีทางรู้ว่า table มี unique สองตัว) จึงต่อฐานข้อมูลจริง

**ต้องเปิดเอง**: ตั้ง WBW_DB_TESTS=1 ก่อนรัน ไม่งั้น skip — `go test ./...` เปล่าๆ
ต้องไม่แอบเขียนฐานข้อมูลของคนอื่นที่บังเอิญเปิดอยู่

ค่าเชื่อมต่ออ่านจาก .env ที่ root ด้วย godotenv ตัวเดียวกับที่เซิร์ฟเวอร์ใช้ — รหัสผ่าน
จึงไม่ต้องโผล่บนบรรทัดคำสั่งเลย

**เขียนอะไรบ้าง**: แถว checkin_feedback ของผู้เข้าร่วมทดสอบ 6931900011 บนฐานที่กำหนด
ในค่าคงที่ข้างล่างเท่านั้น และล้างทิ้งทั้งก่อนและหลังทุกครั้ง ไม่แตะแถว check_in,
ไม่แตะ checkpoint, ไม่แตะบัญชีอื่น
*/

const (
	testUsername = "6931900011"
	// ฐานที่ต้องเช็คอิน + ผู้เข้าร่วมทดสอบเช็คอินไว้แล้ว + ปกติไม่มีความเห็นค้าง
	testCheckpointCheckedIn = 8
	// ฐานบริการ (requires_checkin = false) ที่ผู้เข้าร่วมทดสอบมีแถว check_in อยู่จริง —
	// จำเป็นต้องเป็นแบบนี้ ไม่งั้นแยกไม่ออกว่า ErrNotCheckedIn มาจากตัวกรองหรือมาจาก
	// "ไม่มีแถว check_in" เฉยๆ
	testCheckpointServicePoint = 9
)

// openTestDB — คืน pool + participant uuid
//
// **skip ได้ทางเดียวเท่านั้น: ยังไม่ได้เปิดสวิตช์** · คนที่รัน `go test ./...` เปล่าๆ ไม่ได้ขอ
// อะไรจากฐานข้อมูล การข้ามให้เขาจึงซื่อสัตย์
//
// แต่พอ WBW_DB_TESTS=1 คือการบอกว่า "ฉันขอให้เทสชุดนี้รันจริง" — ตั้งแต่จุดนั้นไปทุกความล้มเหลว
// ต้อง fail ไม่ใช่ skip · เดิม skip หมดทั้ง ต่อไม่ติด/ping ไม่ผ่าน/ไม่มีบัญชีทดสอบ/ข้อมูลตั้งต้น
// ไม่ตรง ซึ่งทำให้ checkout ที่ไม่มี .env รันแล้วขึ้น `ok` ทั้งที่ไม่ได้รันเทสสักตัว (เกิดจริง
// ระหว่างรีวิว) — เทสที่รายงานว่าผ่านโดยไม่ได้ทำอะไรเลยแย่กว่าไม่มีเทส เพราะมันกินความเชื่อใจไปฟรีๆ
func openTestDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	if os.Getenv("WBW_DB_TESTS") != "1" {
		t.Skip("ข้าม: ต้องมี Postgres จริง — ตั้ง WBW_DB_TESTS=1 เพื่อเปิด")
	}
	_ = godotenv.Load("../../.env")

	// ไม่ระบุเอง = ใช้ฐานข้อมูลตัวเดียวกับที่เซิร์ฟเวอร์ต่อ (ประกอบ DSN แบบเดียวกับ
	// config.dsn()) เพราะ fixture ที่เทสพึ่ง — บัญชี 6931900011, ฐานที่ requires_checkin
	// ต่างกัน, แถว check_in — มีอยู่ที่นั่น
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

	var participantID string
	if err := pool.QueryRow(ctx,
		`SELECT user_id::text FROM wbw_user WHERE username = $1`, testUsername).Scan(&participantID); err != nil {
		t.Fatalf("ไม่มีบัญชีทดสอบ %s ในฐานข้อมูลนี้ (%v)", testUsername, err)
	}

	// ข้อมูลตั้งต้นต้องตรงกับที่ค่าคงที่ข้างบนสมมติไว้ ไม่งั้นผลเทสตีความไม่ได้
	assertCheckedIn(t, pool, participantID, testCheckpointCheckedIn, true)
	assertCheckedIn(t, pool, participantID, testCheckpointServicePoint, false)

	return pool, participantID
}

func assertCheckedIn(t *testing.T, pool *pgxpool.Pool, participantID string, checkpointID int, requiresCheckin bool) {
	t.Helper()
	var ok bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS(
			SELECT 1 FROM check_in ci JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id
			 WHERE ci.participant_id = $1::uuid AND ci.checkpoint_id = $2 AND c.requires_checkin = $3
		)`, participantID, checkpointID, requiresCheckin).Scan(&ok)
	// เรียกจาก openTestDB หลังผ่านสวิตช์มาแล้วเท่านั้น — ข้อมูลตั้งต้นไม่ตรงคือ fail ไม่ใช่ skip
	// (ถ้า skip ตรงนี้ ผลที่ได้คือ `ok` ทั้งที่ไม่มีการยืนยันอะไรเลย)
	if err != nil || !ok {
		t.Fatalf("ข้อมูลตั้งต้นไม่ตรง — ฐาน %d ต้องมีแถว check_in ของ %s และ requires_checkin = %v (err=%v)",
			checkpointID, testUsername, requiresCheckin, err)
	}
}

// ล้างความเห็นของผู้เข้าร่วมทดสอบบนฐานที่เทสใช้ ทั้งก่อนเริ่มและหลังจบ
func cleanFeedback(t *testing.T, pool *pgxpool.Pool, participantID string, checkpointIDs ...int) {
	t.Helper()
	wipe := func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM checkin_feedback WHERE participant_id = $1::uuid AND checkpoint_id = ANY($2)`,
			participantID, checkpointIDs)
		if err != nil {
			t.Errorf("ล้างข้อมูลทดสอบไม่สำเร็จ: %v", err)
		}
	}
	wipe()
	t.Cleanup(wipe)
}

func feedbackRequest(clientID string, checkpointID, rating int) model.FeedbackRequest {
	return model.FeedbackRequest{
		ClientID:     clientID,
		CheckpointID: checkpointID,
		Rating:       rating,
		DeviceTime:   time.Now().UTC().Format(time.RFC3339),
	}
}

// บันทึกครั้งแรกสำเร็จ · ส่งซ้ำด้วย client_id เดิม (outbox retry) ต้องได้แถวเดิมคืนมา
// พร้อม created = false ไม่ใช่แถวใหม่และไม่ใช่ 409
func TestFeedbackSubmitCreatesThenRetryReturnsSameRow(t *testing.T) {
	pool, participantID := openTestDB(t)
	cleanFeedback(t, pool, participantID, testCheckpointCheckedIn)
	repo := NewWBWFeedbackRepository(pool)
	ctx := context.Background()

	clientID := newUUID()
	created, isNew, err := repo.Submit(ctx, participantID, feedbackRequest(clientID, testCheckpointCheckedIn, 3))
	if err != nil {
		t.Fatalf("บันทึกครั้งแรกต้องสำเร็จ: %v", err)
	}
	if !isNew {
		t.Fatal("ครั้งแรกต้องเป็นการสร้างใหม่ (created = true)")
	}

	again, isNewAgain, err := repo.Submit(ctx, participantID, feedbackRequest(clientID, testCheckpointCheckedIn, 3))
	if err != nil {
		t.Fatalf("ส่งซ้ำด้วย client_id เดิมต้องไม่ error: %v", err)
	}
	if isNewAgain {
		t.Fatal("ส่งซ้ำต้องไม่สร้างแถวใหม่")
	}
	if again.ID != created.ID {
		t.Fatalf("ส่งซ้ำต้องได้แถวเดิม (%d) ได้ %d", created.ID, again.ID)
	}
}

// ตอบฐานเดิมด้วย client_id ใหม่ = ชน uniq_feedback_participant_checkpoint จริง
// ต้องได้ ErrAlreadyAnswered พร้อม "คำตอบเดิม" ติดมาด้วย (ฟอร์มเอาไปแสดงแบบอ่านอย่างเดียว)
func TestFeedbackSubmitDifferentClientIDSameCheckpointIsAlreadyAnswered(t *testing.T) {
	pool, participantID := openTestDB(t)
	cleanFeedback(t, pool, participantID, testCheckpointCheckedIn)
	repo := NewWBWFeedbackRepository(pool)
	ctx := context.Background()

	first, _, err := repo.Submit(ctx, participantID, feedbackRequest(newUUID(), testCheckpointCheckedIn, 1))
	if err != nil {
		t.Fatalf("บันทึกครั้งแรกต้องสำเร็จ: %v", err)
	}

	_, _, err = repo.Submit(ctx, participantID, feedbackRequest(newUUID(), testCheckpointCheckedIn, 3))
	var dup ErrAlreadyAnswered
	if !errors.As(err, &dup) {
		t.Fatalf("ต้องได้ ErrAlreadyAnswered ได้ %v", err)
	}
	if dup.Existing == nil || dup.Existing.ID != first.ID {
		t.Fatalf("ต้องพาคำตอบเดิม (%d) กลับมาด้วย ได้ %+v", first.ID, dup.Existing)
	}
	if dup.Existing.Rating != 1 {
		t.Fatalf("คำตอบเดิมต้องเป็นของจริง (rating 1) ได้ %d", dup.Existing.Rating)
	}
}

// สอง request ที่ client_id เดียวกันแข่งกัน (outbox flush ซ้อนกับ submit ที่ยังค้างอยู่)
// ต้องไม่มีตัวไหนได้ ErrAlreadyAnswered — retry ไม่ใช่ conflict · นี่คือเคสที่ทำให้ต้อง
// แยก 23505 สองสาเหตุตั้งแต่แรก และเป็นเคสเดียวที่เดินเข้าสาขา "ชนที่ client_id ตัวเอง"
// (เรียงกันธรรมดา SELECT ขั้นที่ 1 จะดักไปก่อนเสมอ)
func TestFeedbackSubmitConcurrentSameClientIDNeverConflicts(t *testing.T) {
	pool, participantID := openTestDB(t)
	cleanFeedback(t, pool, participantID, testCheckpointCheckedIn)
	repo := NewWBWFeedbackRepository(pool)

	clientID := newUUID()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		ids   []int64
		fails []error
	)
	start := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row, _, err := repo.Submit(context.Background(), participantID,
				feedbackRequest(clientID, testCheckpointCheckedIn, 2))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, err)
				return
			}
			ids = append(ids, row.ID)
		}()
	}
	close(start)
	wg.Wait()

	for _, err := range fails {
		var dup ErrAlreadyAnswered
		if errors.As(err, &dup) {
			t.Fatalf("client_id เดียวกันแข่งกันเองต้องไม่ได้ 409: %v", err)
		}
		t.Fatalf("ไม่ควร error: %v", err)
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("ทั้งสอง request ต้องได้แถวเดียวกัน ได้ %v", ids)
	}
}

// ฐานบริการ (requires_checkin = false) ต้องไม่รับความเห็น "แม้จะมีแถว check_in อยู่จริง"
// — เคสนี้แยกตัวกรอง requires_checkin ออกจากเงื่อนไข "ไม่เคยเช็คอิน" ได้ชัดเจน
func TestFeedbackSubmitRejectsServicePointEvenWhenCheckedIn(t *testing.T) {
	pool, participantID := openTestDB(t)
	cleanFeedback(t, pool, participantID, testCheckpointServicePoint)
	repo := NewWBWFeedbackRepository(pool)

	_, _, err := repo.Submit(context.Background(), participantID,
		feedbackRequest(newUUID(), testCheckpointServicePoint, 3))
	if !errors.Is(err, ErrNotCheckedIn) {
		t.Fatalf("ฐานบริการต้องถูกปฏิเสธด้วย ErrNotCheckedIn ได้ %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM checkin_feedback WHERE participant_id = $1::uuid AND checkpoint_id = $2`,
		participantID, testCheckpointServicePoint).Scan(&count); err != nil {
		t.Fatalf("นับแถวไม่สำเร็จ: %v", err)
	}
	if count != 0 {
		t.Fatalf("ต้องไม่มีแถวถูกเขียนเลย ได้ %d", count)
	}
}

// ไม่เคยเช็คอินฐานนี้ = ErrNotCheckedIn · ใช้ uuid สุ่มเป็นผู้ส่งโดยตั้งใจ — ไม่มีแถว
// check_in ใดๆ แน่นอน และเส้นทางนี้ไม่มีวันไปถึง INSERT จึงไม่เขียนอะไรลงฐานข้อมูลเลย
// (ทางเลือกอื่นคือลบแถว check_in ของบัญชีทดสอบทิ้งชั่วคราว ซึ่งแตะข้อมูลร่วมโดยไม่จำเป็น)
func TestFeedbackSubmitRejectsNeverCheckedIn(t *testing.T) {
	pool, _ := openTestDB(t)
	repo := NewWBWFeedbackRepository(pool)

	_, _, err := repo.Submit(context.Background(), newUUID(),
		feedbackRequest(newUUID(), testCheckpointCheckedIn, 3))
	if !errors.Is(err, ErrNotCheckedIn) {
		t.Fatalf("ไม่เคยเช็คอินต้องได้ ErrNotCheckedIn ได้ %v", err)
	}
}
