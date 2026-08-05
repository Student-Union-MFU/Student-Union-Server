package repository

import (
	"context"
	"os"
	"strconv"
	"sync"
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

// เทสนี้พิสูจน์ Important 1 จากรีวิว: การกดครั้งแรกสุดของคนคนหนึ่ง (ยังไม่มีแถวใน sos_event
// เลย) ไม่มีอะไรให้ FOR UPDATE ล็อก สอง Raise ที่มาพร้อมกันจึงเห็น ErrNoRows เหมือนกันแล้ววิ่ง
// เข้า branch INSERT ทั้งคู่ ไปชนกันที่ sos_one_open_per_user จริง — ตัวที่แพ้ต้องไม่ได้ 23505
// ดิบๆ กลับไป ต้อง retry แล้วเจอแถวที่ตัวชนะสร้างไว้ ตอบ created = false แทน
func TestRaiseConcurrentOnBrandNewParticipantNeverLeaksARaw23505(t *testing.T) {
	skipWithoutDB(t)
	repo, participant := newSOSTestRepo(t)

	clientIDs := []string{
		"ffffffff-0000-0000-0000-000000000001",
		"ffffffff-0000-0000-0000-000000000002",
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ids     []int64
		created []bool
		fails   []error
	)
	start := make(chan struct{})
	for _, cid := range clientIDs {
		wg.Add(1)
		go func(cid string) {
			defer wg.Done()
			<-start
			c, isNew, err := repo.Raise(context.Background(), participant, model.SOSRequest{
				ClientID: cid, DeviceTime: "2026-08-06T10:00:00Z",
			}, nil, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, err)
				return
			}
			ids = append(ids, c.ID)
			created = append(created, isNew)
		}(cid)
	}
	close(start)
	wg.Wait()

	for _, err := range fails {
		t.Fatalf("กดครั้งแรกพร้อมกันสองฝั่งต้องไม่มีใคร error เลย (โดยเฉพาะ 23505 ดิบๆ): %v", err)
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("ทั้งสองฝั่งต้องลงเอยที่แถวเดียวกัน ได้ %v", ids)
	}
	newCount := 0
	for _, c := range created {
		if c {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("ต้องมีแค่ฝั่งเดียวที่ created = true (อีกฝั่งต้อง retry แล้วเจอแถวเดิม) ได้ %d จาก %v", newCount, created)
	}

	var rowCount int
	if err := repo.db.QueryRow(context.Background(),
		`SELECT count(*) FROM sos_event WHERE participant_id = $1::uuid`, participant).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("ต้องมีแถว sos_event แค่แถวเดียวสำหรับคนนี้ ได้ %d", rowCount)
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

func TestStaffFeedScopesByAssignedBaseButFallsBackWhenNobodyIsAssigned(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)

	assigned := seedStaffAtCheckpoint(t, 2)  // staff ประจำฐาน 2
	elsewhere := seedStaffAtCheckpoint(t, 5) // staff ประจำฐาน 5

	// เคสที่ฐาน 2 — คนประจำฐาน 2 เห็น คนประจำฐาน 5 ไม่เห็น
	if _, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "f0000000-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, intPtr(2), strPtr("gps")); err != nil {
		t.Fatal(err)
	}

	mine, err := repo.StaffFeed(ctx, assigned, "staff", "")
	if err != nil || len(mine) != 1 {
		t.Fatalf("staff ประจำฐาน 2 ต้องเห็นเคสของฐานตัวเอง ได้ %d เคส err=%v", len(mine), err)
	}
	theirs, err := repo.StaffFeed(ctx, elsewhere, "staff", "")
	if err != nil || len(theirs) != 0 {
		t.Fatalf("staff ฐานอื่นต้องไม่เห็น ได้ %d เคส", len(theirs))
	}
}

func TestStaffFeedShowsEveryCaseToAdminAndToMedical(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)
	admin := seedUserWithRole(t, "admin", "")
	medic := seedUserWithRole(t, "staff", "medical")

	if _, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "f0000000-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:00:00Z",
	}, intPtr(2), strPtr("gps")); err != nil {
		t.Fatal(err)
	}

	for name, u := range map[string]struct{ id, role string }{
		"admin":   {admin, "admin"},
		"medical": {medic, "staff"},
	} {
		got, err := repo.StaffFeed(ctx, u.id, u.role, "")
		if err != nil || len(got) != 1 {
			t.Fatalf("%s ต้องเห็นทุกเคส ได้ %d เคส err=%v", name, len(got), err)
		}
	}
}

func TestStaffFeedFallsBackToEveryoneWhenTheBaseHasNoAssignedStaff(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)
	unrelated := seedStaffAtCheckpoint(t, 5)

	// ฐาน 7 ไม่มีใครถูก assign เลย
	if _, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "f0000000-0000-0000-0000-000000000003", DeviceTime: "2026-08-06T10:00:00Z",
	}, intPtr(7), strPtr("gps")); err != nil {
		t.Fatal(err)
	}
	got, err := repo.StaffFeed(ctx, unrelated, "staff", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("ฐานที่ไม่มีคนประจำ ทุกคนต้องเห็น ได้ %d เคส err=%v", len(got), err)
	}
}

func TestStaffFeedShowsCasesWithNoBaseToEveryone(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)
	anyStaff := seedStaffAtCheckpoint(t, 5)

	if _, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "f0000000-0000-0000-0000-000000000004", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, strPtr("none")); err != nil {
		t.Fatal(err)
	}
	got, err := repo.StaffFeed(ctx, anyStaff, "staff", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("เคสที่ไม่มีพิกัดเลย ทุกคนต้องเห็น ได้ %d เคส err=%v", len(got), err)
	}
}

// ==== รอบแก้ตาม code review: cursor ชนกันได้จริง + สี่เส้นทางความปลอดภัยไม่มีเทสถาวร ====

// เทสนี้พิสูจน์ข้อสำคัญที่ 1 จากรีวิว: updated_at ไม่ unique — now() นิ่งตลอดทรานแซกชันเดียว
// ไม่ใช่ clock_timestamp() ที่ขยับทุก statement สอง sos_event ที่ถูกอัปเดตจากทรานแซกชันคนละตัว
// แต่ในช่วงเวลาเดียวกันจึงได้ updated_at เท่ากันเป๊ะได้จริง (เคสยิงเข้าพร้อมกันหลายเคสคือ
// สถานการณ์ที่ฟีเจอร์นี้มีไว้รับมือ) เทียบด้วย > ตัวเดียวแล้ว cursor ของฝั่งเรียกลงล็อกที่ค่านั้น
// พอดี ทำให้แถวที่เหลือตกหล่นถาวร (ไม่มีการแบ่งหน้าที่จะดึงกลับมาได้) บังคับ updated_at ให้ชน
// กันตรงๆ ด้วย UPDATE แทนที่จะหวังให้ now() ชนกันเอง (ไม่น่าเชื่อถือพอจะเป็นเทส)
func TestStaffFeedCursorSurvivesATieOnUpdatedAt(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo1, p1 := newSOSTestRepo(t)
	repo2, p2 := newSOSTestRepo(t)
	admin := seedUserWithRole(t, "admin", "")

	a, _, err := repo1.Raise(ctx, p1, model.SOSRequest{
		ClientID: "c0de0001-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := repo2.Raise(ctx, p2, model.SOSRequest{
		ClientID: "c0de0001-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	tie := "2026-08-06T10:05:00Z"
	if _, err := repo1.db.Exec(ctx,
		`UPDATE sos_event SET updated_at = $1::timestamptz WHERE id = $2`, tie, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo1.db.Exec(ctx,
		`UPDATE sos_event SET updated_at = $1::timestamptz WHERE id = $2`, tie, b.ID); err != nil {
		t.Fatal(err)
	}

	// id เล็กกว่าคือ "เก่ากว่า" ในความหมายของ (updated_at, id) แม้ updated_at ชนกันเป๊ะ
	older, newer := a, b
	if b.ID < a.ID {
		older, newer = b, a
	}

	cursor := tie + "|" + strconv.FormatInt(older.ID, 10)
	page, err := repo1.StaffFeed(ctx, admin, "admin", cursor)
	if err != nil {
		t.Fatal(err)
	}
	sawNewer, sawOlder := false, false
	for _, c := range page {
		if c.ID == newer.ID {
			sawNewer = true
		}
		if c.ID == older.ID {
			sawOlder = true
		}
	}
	if !sawNewer {
		t.Fatalf("แถว id=%d มี updated_at ชนกับ cursor เป๊ะแต่ id มากกว่า ต้องยังเห็นได้หลัง poll รอบถัดไป", newer.ID)
	}
	if sawOlder {
		t.Fatalf("แถว id=%d คือแถวที่ cursor ชี้ไว้แล้ว ต้องไม่ถูกส่งซ้ำอีก", older.ID)
	}
}

// เทสนี้พิสูจน์ข้อสำคัญที่ 2 จากรีวิว ข้อแรก: กติกาข้อ 4 ของ staffVisibility (accuracy_m > 200)
// แยกให้ชัดจากกติกาข้อ 3 ("ไม่มีใครประจำฐาน") โดยตั้งใจให้ฐานของเคสมีคนอื่นประจำอยู่จริง
// (home) — ถ้า elsewhere ยังเห็นเคส accuracy แย่ได้ ต้องเป็นเพราะกติกาข้อ 4 เท่านั้น ไม่ใช่ข้อ 3
func TestStaffFeedAccuracyOver200IsVisibleAcrossBasesButGoodAccuracyIsNot(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	home := seedStaffAtCheckpoint(t, 6)       // เจ้าของฐาน 6 ตัวจริง
	elsewhere := seedStaffAtCheckpoint(t, 10) // ประจำฐานอื่น ไม่ใช่ฐาน 6
	_ = home

	// เปิด pool ของผู้เข้าร่วมทั้งสองคน "ก่อน" Raise ทั้งคู่เสมอ — ห้ามสลับ เพราะ
	// newSOSTestRepo เรียก openSOSTestDB ซึ่งลบ sos_event ที่ client_id อยู่ใน
	// testSOSClientIDs ทิ้งทุกครั้งที่เปิด (กันรันก่อนหน้าตายกลางคัน) ถ้า Raise เคสแรกไปแล้ว
	// ค่อยเรียก newSOSTestRepo รอบสอง เคสแรกที่เพิ่งสร้าง (client_id อยู่ในลิสต์นั้นด้วย)
	// จะโดนลบทิ้งกลางเทสเงียบๆ — เจอบั๊กนี้จริงตอนเขียนเทสนี้ครั้งแรก ดู `openSOSTestDB` ด้านล่าง
	repoBad, pBad := newSOSTestRepo(t)
	repoGood, pGood := newSOSTestRepo(t)

	badAcc := 250.0
	bad, _, err := repoBad.Raise(ctx, pBad, model.SOSRequest{
		ClientID: "acc00001-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
		AccuracyM: &badAcc,
	}, intPtr(6), strPtr("gps"))
	if err != nil {
		t.Fatal(err)
	}

	goodAcc := 50.0
	good, _, err := repoGood.Raise(ctx, pGood, model.SOSRequest{
		ClientID: "acc00001-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:00:00Z",
		AccuracyM: &goodAcc,
	}, intPtr(6), strPtr("gps"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := repoBad.StaffFeed(ctx, elsewhere, "staff", "")
	if err != nil {
		t.Fatal(err)
	}
	seenBad, seenGood := false, false
	for _, c := range got {
		if c.ID == bad.ID {
			seenBad = true
		}
		if c.ID == good.ID {
			seenGood = true
		}
	}
	if !seenBad {
		t.Fatal("accuracy_m=250 (>200) ต้องเห็นได้แม้ประจำฐานอื่น และฐาน 6 มีคนอื่นดูแลอยู่แล้วจริง")
	}
	if seenGood {
		t.Fatal("accuracy_m=50 (แม่นพอ) ที่ฐานซึ่งมีคนอื่นประจำอยู่แล้ว ต้องไม่เห็น")
	}
}

// เทสนี้พิสูจน์ข้อสำคัญที่ 2 จากรีวิว ข้อสอง: เงื่อนไขทั้งสามของ health-data gate ต้องจริง
// พร้อมกันทั้งหมด — เคสฐานเห็นได้ครบ แล้วปลดทีละเงื่อนไข สามกรณีลบ ไม่ใช่กรณีเดียว
func TestStaffFeedHealthDataGateNeedsConsentUnresolvedAndNotForOther(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	admin := seedUserWithRole(t, "admin", "")

	// เปิด pool ของผู้เข้าร่วมทั้งสามคน "ก่อน" Raise ทั้งสามเคสเสมอ — เหตุผลเดียวกับเทส
	// accuracy ด้านบน: newSOSTestRepo แต่ละครั้งลบ sos_event ที่ client_id ตรงกับ
	// testSOSClientIDs ทิ้งไปด้วย เคสที่ raise ไปก่อนจะโดนลบถ้าเรียก newSOSTestRepo แทรกทีหลัง
	repo, participant := newSOSTestRepo(t)
	repo2, participant2 := newSOSTestRepo(t)
	repo3, participant3 := newSOSTestRepo(t)
	seedConsent(t, participant, true)
	seedHealthDetails(t, participant, "O+", "โรคหัวใจ")
	seedConsent(t, participant2, true)
	seedHealthDetails(t, participant2, "A+", "")
	seedConsent(t, participant3, false)
	seedHealthDetails(t, participant3, "B+", "")

	// เคสฐาน: consent=true, ยังเปิดอยู่, for_other=false — ต้องเห็นข้อมูลสุขภาพครบ
	c, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "ea700001-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := findCase(t, repo, admin, "admin", c.ID)
	if row.BloodType == nil || *row.BloodType != "O+" {
		t.Fatalf("consent=true, unresolved, for_other=false ต้องเห็น blood_type ได้ %v", row.BloodType)
	}
	if row.HealthNotes == nil || *row.HealthNotes == "" {
		t.Fatal("ต้องเห็น health_notes ด้วยเงื่อนไขเดียวกัน")
	}

	// ปลดเงื่อนไขที่ 1: resolved — ปิดเคสเดิม ข้อมูลสุขภาพต้องหายแม้ consent ยัง true
	if err := repo.Resolve(ctx, c.ID, admin, "helped"); err != nil {
		t.Fatal(err)
	}
	row = findCase(t, repo, admin, "admin", c.ID)
	if row.BloodType != nil || row.HealthNotes != nil {
		t.Fatalf("ปิดเคสแล้วต้องไม่เห็นข้อมูลสุขภาพอีก ได้ blood=%v notes=%v", row.BloodType, row.HealthNotes)
	}

	// ปลดเงื่อนไขที่ 2: for_other — เคสใหม่ consent=true, unresolved, แต่ for_other=true
	c2, _, err := repo2.Raise(ctx, participant2, model.SOSRequest{
		ClientID: "ea700001-0000-0000-0000-000000000002", DeviceTime: "2026-08-06T10:00:00Z",
		ForOther: true,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	row = findCase(t, repo2, admin, "admin", c2.ID)
	if row.BloodType != nil {
		t.Fatalf("for_other=true ต้องไม่เห็น blood_type แม้ consent=true และยัง unresolved ได้ %v", *row.BloodType)
	}

	// ปลดเงื่อนไขที่ 3: consent — เคสใหม่ unresolved, for_other=false, แต่ consent=false
	c3, _, err := repo3.Raise(ctx, participant3, model.SOSRequest{
		ClientID: "ea700001-0000-0000-0000-000000000003", DeviceTime: "2026-08-06T10:00:00Z",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	row = findCase(t, repo3, admin, "admin", c3.ID)
	if row.BloodType != nil {
		t.Fatalf("consent_health_data=false ต้องไม่เห็น blood_type ได้ %v", *row.BloodType)
	}
}

// เทสนี้พิสูจน์ข้อสำคัญที่ 2 จากรีวิว ข้อสาม: PushAudience ตัดสินว่าใครถูกปลุกเรื่องเคสฉุกเฉิน
// จริง — assertion ที่สำคัญที่สุดคือบรรทัดสุดท้าย: กลุ่มเพื่อนต้องไม่มี token ของคนกดเอง
func TestPushAudienceReturnsBaseStaffCentralStaffAndGroupmatesExcludingThePresser(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()

	groupID := seedGroup(t)
	repo, presser := newSOSTestRepo(t)
	_, groupmate := newSOSTestRepo(t)
	setParticipantGroup(t, presser, groupID)
	setParticipantGroup(t, groupmate, groupID)

	baseStaff := seedStaffAtCheckpoint(t, 11)
	medic := seedUserWithRole(t, "staff", "medical")

	baseToken := seedDeviceToken(t, baseStaff)
	medicToken := seedDeviceToken(t, medic)
	presserToken := seedDeviceToken(t, presser)
	groupmateToken := seedDeviceToken(t, groupmate)

	c, _, err := repo.Raise(ctx, presser, model.SOSRequest{
		ClientID: "add00001-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, intPtr(11), strPtr("gps"))
	if err != nil {
		t.Fatal(err)
	}

	a, err := repo.PushAudience(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(a.StaffTokens, baseToken) {
		t.Fatalf("StaffTokens ต้องมี token ของเจ้าหน้าที่ประจำฐาน ได้ %v", a.StaffTokens)
	}
	if !containsStr(a.CentralTokens, medicToken) {
		t.Fatalf("CentralTokens ต้องมี token ของ medical ได้ %v", a.CentralTokens)
	}
	if !containsStr(a.GroupTokens, groupmateToken) {
		t.Fatalf("GroupTokens ต้องมี token ของเพื่อนร่วมกลุ่ม ได้ %v", a.GroupTokens)
	}
	if containsStr(a.GroupTokens, presserToken) {
		t.Fatal("GroupTokens ต้องไม่มี token ของคนกดเอง — เขาไม่ควรได้แจ้งเตือนเรื่องที่ตัวเองกด")
	}
}

// เทสนี้พิสูจน์ข้อสำคัญที่ 2 จากรีวิว ข้อสี่: CanStaffSee ต้องให้ผลตรงกับ StaffFeed เพราะเป็น
// ประตูของ Ack/Resolve — เจ้าหน้าที่ฐานอื่นเห็นเคสไม่ได้ ต้อง Ack/Resolve ไม่ได้ด้วย
func TestCanStaffSeeMatchesStaffFeedVisibility(t *testing.T) {
	skipWithoutDB(t)
	ctx := context.Background()
	repo, participant := newSOSTestRepo(t)
	home := seedStaffAtCheckpoint(t, 12)
	elsewhere := seedStaffAtCheckpoint(t, 1) // ฐานอื่น และฐาน 12 มี home ประจำอยู่แล้วจริง
	admin := seedUserWithRole(t, "admin", "")
	medic := seedUserWithRole(t, "staff", "medical")

	c, _, err := repo.Raise(ctx, participant, model.SOSRequest{
		ClientID: "ca500001-0000-0000-0000-000000000001", DeviceTime: "2026-08-06T10:00:00Z",
	}, intPtr(12), strPtr("gps"))
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		id, role string
		want     bool
	}{
		"เจ้าหน้าที่ประจำฐาน": {home, "staff", true},
		"admin":   {admin, "admin", true},
		"medical": {medic, "staff", true},
		"เจ้าหน้าที่ฐานอื่น": {elsewhere, "staff", false},
	}
	for name, tc := range cases {
		ok, err := repo.CanStaffSee(ctx, tc.id, tc.role, c.ID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ok != tc.want {
			t.Fatalf("%s: ต้องการ %v ได้ %v", name, tc.want, ok)
		}
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
	"ffffffff-0000-0000-0000-000000000001", "ffffffff-0000-0000-0000-000000000002",
	"f0000000-0000-0000-0000-000000000001", "f0000000-0000-0000-0000-000000000002",
	"f0000000-0000-0000-0000-000000000003", "f0000000-0000-0000-0000-000000000004",
	"c0de0001-0000-0000-0000-000000000001", "c0de0001-0000-0000-0000-000000000002",
	"acc00001-0000-0000-0000-000000000001", "acc00001-0000-0000-0000-000000000002",
	"ea700001-0000-0000-0000-000000000001", "ea700001-0000-0000-0000-000000000002",
	"ea700001-0000-0000-0000-000000000003",
	"add00001-0000-0000-0000-000000000001",
	"ca500001-0000-0000-0000-000000000001",
}

// openSOSTestDB — เปิด pool ทดสอบหนึ่งตัว แล้วล้างเคสทดสอบเก่า (ถ้ามีค้างจากรันก่อนหน้า
// ที่ตายกลางคัน) ก่อนเทสเริ่มจริง
//
// ข้อควรระวังสำหรับเทสที่จะเขียนเพิ่ม: ฟังก์ชันนี้ถูกเรียกจากทุก seed helper ในไฟล์นี้
// (newSOSTestRepo, seedStaffAtCheckpoint, seedUserWithRole, seedDeviceToken, ฯลฯ) และการ
// ลบ sos_event ที่ client_id ตรงกับ testSOSClientIDs ด้านล่างนี้ "ไม่ได้เกิดแค่ครั้งแรกที่เปิด
// pool ของเทส" — เกิดทุกครั้งที่มีการเรียก seed helper ตัวไหนก็ตามในไฟล์นี้ ถ้าเทสหนึ่ง raise
// เคสไปแล้ว (ด้วย client_id ที่อยู่ในลิสต์) แล้วค่อยเรียก seed helper ตัวใหม่ (เช่น
// newSOSTestRepo ของผู้เข้าร่วมคนที่สอง) เคสที่เพิ่ง raise จะโดนลบทิ้งเงียบๆ กลางเทส — เจอบั๊กนี้
// จริงตอนเขียน TestStaffFeedAccuracyOver200IsVisibleAcrossBasesButGoodAccuracyIsNot ครั้งแรก
// (ผลลัพธ์ StaffFeed ว่างเปล่าทั้งที่ควรมี 1 แถว) กติกาที่ปลอดภัย: เรียก seed helper ทุกตัวที่
// ต้องใช้ "ก่อน" Raise เคสแรกเสมอ อย่าแทรก seed helper ตัวใหม่ระหว่างเคสที่ raise ไปแล้วกับ
// เคสที่ยังไม่ได้ใช้งาน
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

	// ลงทะเบียนล้างทิ้งทันทีหลัง insert แถวแรกสำเร็จ — ถ้า insert แถวถัดไป (participant_profile)
	// พังแล้ว t.Fatalf แถว wbw_user นี้ต้องไม่ค้าง ไม่ใช่รอไปลงทะเบียนหลัง insert ทุกแถวเสร็จ
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

	if _, err := pool.Exec(ctx,
		`INSERT INTO participant_profile (user_id) VALUES ($1::uuid)`, userID); err != nil {
		t.Fatalf("สร้างโปรไฟล์ผู้เข้าร่วมทดสอบไม่สำเร็จ: %v", err)
	}

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

// seedStaffAtCheckpoint — เจ้าหน้าที่หนึ่งคน ถูก assign เข้าฐานที่ระบุผ่าน checkpoint_staff
// ใช้พิสูจน์กติกาข้อ 1 ของ staffVisibility: เห็นเฉพาะฐานที่ตัวเองถูก assign
//
// ทรงเดียวกับ seedStaffUser: ลงทะเบียน t.Cleanup ทันทีหลัง insert wbw_user สำเร็จ ก่อนจะ
// insert checkpoint_staff ต่อ — ถ้ารอไปลงทะเบียนหลัง insert ทุกแถวเสร็จแล้วแถวถัดไปพังกลางคัน
// แถว wbw_user จะค้างไม่ถูกลบ (Task 3 โดนมาแล้วกับ newSOSTestRepo ดู comment ด้านบน)
func seedStaffAtCheckpoint(t *testing.T, checkpointID int) string {
	t.Helper()
	pool := openSOSTestDB(t)
	ctx := context.Background()

	username := "sos-staff-cp-" + newUUID()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO wbw_user (username, password_hash, role, display_name)
		VALUES ($1, 'x', 'staff', 'SOS Test Staff At Checkpoint')
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
			`DELETE FROM checkpoint_staff WHERE user_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างการ assign ฐานของเจ้าหน้าที่ทดสอบไม่สำเร็จ: %v", err)
		}
		if _, err := pool.Exec(cctx,
			`DELETE FROM wbw_user WHERE user_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างเจ้าหน้าที่ทดสอบไม่สำเร็จ: %v", err)
		}
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO checkpoint_staff (checkpoint_id, user_id) VALUES ($1, $2::uuid)`,
		checkpointID, userID); err != nil {
		t.Fatalf("assign เจ้าหน้าที่ทดสอบเข้าฐาน %d ไม่สำเร็จ: %v", checkpointID, err)
	}

	return userID
}

// seedUserWithRole — บัญชีที่มี role (และ staff_role ถ้าระบุ) ตามต้องการ ใช้พิสูจน์ว่า admin
// กับ staff บทบาท medical/security เห็นทุกเคสโดยไม่สนใจฐาน (seesEverything)
//
// staffRole ว่าง ("") แปลว่าไม่ต้องมีแถวใน wbw_staff เลย — กรณี admin ไม่จำเป็นต้องมี staff_role
// ทรงเดียวกับ seedStaffUser: ลงทะเบียน t.Cleanup ทันทีหลัง insert wbw_user สำเร็จ ก่อนจะ
// insert wbw_staff ต่อ (ถ้ามี) กันแถว wbw_user ค้างถ้า insert ถัดไปพัง
func seedUserWithRole(t *testing.T, role, staffRole string) string {
	t.Helper()
	pool := openSOSTestDB(t)
	ctx := context.Background()

	username := "sos-" + role + "-" + newUUID()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO wbw_user (username, password_hash, role, display_name)
		VALUES ($1, 'x', $2::user_role, 'SOS Test User With Role')
		RETURNING user_id::text`, username, role).Scan(&userID); err != nil {
		t.Fatalf("สร้างผู้ใช้ทดสอบ (role=%s) ไม่สำเร็จ: %v", role, err)
	}

	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx,
			`DELETE FROM sos_event WHERE acked_by = $1::uuid OR resolved_by = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างเคส SOS ที่ผูกกับผู้ใช้ทดสอบไม่สำเร็จ: %v", err)
		}
		if _, err := pool.Exec(cctx,
			`DELETE FROM wbw_staff WHERE user_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้าง wbw_staff ของผู้ใช้ทดสอบไม่สำเร็จ: %v", err)
		}
		if _, err := pool.Exec(cctx,
			`DELETE FROM wbw_user WHERE user_id = $1::uuid`, userID); err != nil {
			t.Errorf("ล้างผู้ใช้ทดสอบไม่สำเร็จ: %v", err)
		}
	})

	if staffRole != "" {
		if _, err := pool.Exec(ctx,
			`INSERT INTO wbw_staff (user_id, staff_role) VALUES ($1::uuid, $2::staff_role)`,
			userID, staffRole); err != nil {
			t.Fatalf("สร้าง wbw_staff (staff_role=%s) ไม่สำเร็จ: %v", staffRole, err)
		}
	}

	return userID
}

// seedGroup — กลุ่มทดสอบใหม่หนึ่งกลุ่ม เลข group_number เลือกจาก MAX+1 กันชนของเดิม (คอลัมน์
// unique ทั้งตาราง ไม่มี default ให้ฐานข้อมูลสุ่มเอง) ลบทิ้งตอนจบเทสเสมอ
//
// ต้องเรียกตัวนี้ "ก่อน" newSOSTestRepo ของผู้เข้าร่วมที่จะเข้ากลุ่มนี้เสมอ (ดูลำดับเรียกใน
// เทสที่ใช้จริง) — LIFO: cleanup ที่ลงทะเบียนก่อนจะรันทีหลัง ต้องให้แถว participant_profile
// ที่อ้างกลุ่มนี้หายไปก่อน (cascade มาจาก wbw_user ที่ newSOSTestRepo ลบให้) แล้ว
// participant_group ถึงจะลบได้โดยไม่ชน FK — group_id ไม่มี ON DELETE CASCADE ฝั่งนั้น
func seedGroup(t *testing.T) int {
	t.Helper()
	pool := openSOSTestDB(t)
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
	return groupID
}

// setParticipantGroup — ตั้ง group_id ให้ผู้เข้าร่วมทดสอบที่มีอยู่แล้ว ไม่ต้อง cleanup แยก —
// แถวหายไปทั้งแถวเมื่อ newSOSTestRepo ลบ wbw_user ของเจ้าของ (cascade ไป participant_profile)
func setParticipantGroup(t *testing.T, participantID string, groupID int) {
	t.Helper()
	pool := openSOSTestDB(t)
	if _, err := pool.Exec(context.Background(),
		`UPDATE participant_profile SET group_id = $1 WHERE user_id = $2::uuid`,
		groupID, participantID); err != nil {
		t.Fatalf("ตั้งกลุ่มให้ผู้เข้าร่วมทดสอบไม่สำเร็จ: %v", err)
	}
}

// seedDeviceToken — token อุปกรณ์หนึ่งอันของผู้ใช้ที่ระบุ (participant หรือ staff ก็ได้)
// ลบทิ้งตอนจบเทสเสมอ — ปลอดภัยไม่ว่าจะรันก่อนหรือหลังตัวผู้ใช้ถูกลบ (device_token.user_id
// เป็น ON DELETE CASCADE จาก wbw_user อยู่แล้ว ลบซ้ำด้วย token ที่หายไปแล้วเป็นแค่ no-op)
func seedDeviceToken(t *testing.T, userID string) string {
	t.Helper()
	pool := openSOSTestDB(t)
	ctx := context.Background()

	token := "sos-device-" + newUUID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO device_token (token, user_id, platform) VALUES ($1, $2::uuid, 'ios')`,
		token, userID); err != nil {
		t.Fatalf("สร้าง device token ทดสอบไม่สำเร็จ: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM device_token WHERE token = $1`, token); err != nil {
			t.Errorf("ล้าง device token ทดสอบไม่สำเร็จ: %v", err)
		}
	})
	return token
}

// seedConsent — ตั้งค่า consent_health_data ของผู้เข้าร่วมทดสอบที่ระบุ ไม่ต้อง cleanup แยก
// (เหตุผลเดียวกับ setParticipantGroup — cascade ไปกับ wbw_user ที่ newSOSTestRepo ลบให้)
func seedConsent(t *testing.T, participantID string, consentHealthData bool) {
	t.Helper()
	pool := openSOSTestDB(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO consent (user_id, consent_health_data) VALUES ($1::uuid, $2)`,
		participantID, consentHealthData); err != nil {
		t.Fatalf("ตั้งค่า consent ทดสอบไม่สำเร็จ: %v", err)
	}
}

// seedHealthDetails — ข้อมูลสุขภาพของผู้เข้าร่วมทดสอบที่ระบุ ไม่ต้อง cleanup แยกด้วยเหตุผลเดียวกัน
func seedHealthDetails(t *testing.T, participantID, bloodType, chronicDisease string) {
	t.Helper()
	pool := openSOSTestDB(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO health_details (user_id, blood_type, chronic_disease)
		 VALUES ($1::uuid, $2::blood_type, NULLIF($3,''))`,
		participantID, bloodType, chronicDisease); err != nil {
		t.Fatalf("ตั้งค่าข้อมูลสุขภาพทดสอบไม่สำเร็จ: %v", err)
	}
}

// findCase — หาเคสจาก id ในผลลัพธ์ StaffFeed ของ viewer ที่ระบุ — เทสไม่ต้องไล่ index เอง
func findCase(t *testing.T, repo *WBWSOSRepository, viewerID, role string, id int64) *model.SOSStaffCase {
	t.Helper()
	feed, err := repo.StaffFeed(context.Background(), viewerID, role, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range feed {
		if feed[i].ID == id {
			return &feed[i]
		}
	}
	t.Fatalf("ไม่เจอเคส id=%d ในฟีดของ %s", id, viewerID)
	return nil
}

// containsStr — เช็คว่า token ที่ต้องการอยู่ใน []string ที่ PushAudience คืนมาไหม (ไม่สนลำดับ)
func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }
