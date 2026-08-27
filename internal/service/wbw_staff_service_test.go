package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"su-server/internal/model"
)

/*
เทสชุดนี้ตรึงเงื่อนไขที่ spec สั่งไว้ตรงๆ: "Checkin creates a notification only when
already_checked_in is false" (docs/superpowers/specs/2026-08-02-checkin-feedback-design.md)

สแกนซ้ำคนเดิมเกิดจริงตลอดเวลาหน้างาน (คิวยาว เจ้าหน้าที่ไม่แน่ใจว่าสแกนไปแล้วหรือยัง
ก็สแกนใหม่) ถ้าเงื่อนไขนี้พลาด ผู้เข้าร่วมคนหนึ่งจะได้แจ้งเตือน "ให้คะแนนฐานนี้" ซ้ำๆ
ทั้งที่ตอบไปแล้ว และ badge กระดิ่งจะเด้งกลับทุกครั้ง
*/

// ตัวปลอมของ repository — คุม AlreadyCheckedIn/ParticipantID ได้ตามเคสที่ต้องการ
type fakeStaffRepo struct {
	result *model.CheckinResult
	err    error
}

func (f *fakeStaffRepo) Checkpoints(context.Context, string, string) ([]model.StaffCheckpoint, error) {
	return nil, nil
}

func (f *fakeStaffRepo) Checkin(context.Context, string, int, *string, *int) (*model.CheckinResult, error) {
	return f.result, f.err
}

func (f *fakeStaffRepo) CheckpointName(context.Context, int) string {
	return "ฐานทดสอบ"
}

// ตัวปลอมของบริการแจ้งเตือน — ส่งคำขอที่ได้รับเข้า channel ให้เทสรอได้จริง
// (notifyFeedback ทำงานใน goroutine แยก assert ทันทีหลัง Checkin คืนค่าจะแข่งกันเสมอ)
type fakeNotifier struct {
	created chan model.NotificationRequest
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{created: make(chan model.NotificationRequest, 4)}
}

func (f *fakeNotifier) Create(_ context.Context, req model.NotificationRequest, _ string) (*model.Notification, error) {
	f.created <- req
	return &model.Notification{}, nil
}

type fakePusher struct{ sent chan string }

func newFakePusher() *fakePusher { return &fakePusher{sent: make(chan string, 4)} }

func (f *fakePusher) SendUserPush(_ context.Context, userID, _, _ string, _ map[string]string) {
	f.sent <- userID
}

func newStaffServiceUnderTest(res *model.CheckinResult) (*WBWStaffService, *fakeNotifier, *fakePusher) {
	noti, push := newFakeNotifier(), newFakePusher()
	return NewWBWStaffService(&fakeStaffRepo{result: res}, noti, push), noti, push
}

func ptr[T any](v T) *T { return &v }

// เช็คอินครั้งแรก → ต้องมีแจ้งเตือนขอความเห็นพร้อม ref_id = เลขฐาน (ฝั่งแอปอ่านค่านี้
// ไปเปิดฟอร์มให้ถูกฐาน) และต้องยิง push ให้คนเดียวกัน
func TestCheckinNotifiesOnFirstCheckin(t *testing.T) {
	svc, noti, push := newStaffServiceUnderTest(&model.CheckinResult{
		AlreadyCheckedIn: false,
		ParticipantID:    "11111111-1111-1111-1111-111111111111",
	})

	if _, err := svc.Checkin(context.Background(), "staff-1", model.StaffCheckinRequest{
		CheckpointID: ptr(7), Bib: ptr(123),
	}); err != nil {
		t.Fatalf("Checkin ไม่ควร error: %v", err)
	}

	select {
	case req := <-noti.created:
		if req.RefID == nil || *req.RefID != "7" {
			t.Fatalf("ref_id ต้องเป็นเลขฐาน ได้ %v", req.RefID)
		}
		if req.Type == nil || *req.Type != "checkin_feedback" {
			t.Fatalf("type ต้องเป็น checkin_feedback ได้ %v", req.Type)
		}
		if req.AudienceID == nil || *req.AudienceID != "11111111-1111-1111-1111-111111111111" {
			t.Fatalf("แจ้งเตือนต้องส่งถึงผู้เข้าร่วมที่เพิ่งถูกสแกน ได้ %v", req.AudienceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("เช็คอินครั้งแรกต้องสร้างแจ้งเตือนขอความเห็น")
	}

	select {
	case <-push.sent:
	case <-time.After(2 * time.Second):
		t.Fatal("เช็คอินครั้งแรกต้องยิง push ด้วย")
	}
}

// สแกนซ้ำคนเดิม (already_checked_in = true) → ห้ามมีทั้งแจ้งเตือนและ push
//
// รอจริงๆ ก่อนสรุปว่า "ไม่มี" — notifyFeedback อยู่ใน goroutine ถ้า assert ทันทีที่ Checkin
// คืนค่า เทสจะผ่านแม้โค้ดยิงแจ้งเตือนออกไปแล้ว เพียงแต่ยังไม่ทันเดินถึง
func TestCheckinDoesNotNotifyWhenAlreadyCheckedIn(t *testing.T) {
	svc, noti, push := newStaffServiceUnderTest(&model.CheckinResult{
		AlreadyCheckedIn: true,
		ParticipantID:    "11111111-1111-1111-1111-111111111111",
	})

	if _, err := svc.Checkin(context.Background(), "staff-1", model.StaffCheckinRequest{
		CheckpointID: ptr(7), Bib: ptr(123),
	}); err != nil {
		t.Fatalf("Checkin ไม่ควร error: %v", err)
	}

	select {
	case req := <-noti.created:
		t.Fatalf("สแกนซ้ำคนเดิมต้องไม่แจ้งเตือนอีก แต่ได้ %+v", req)
	case <-push.sent:
		t.Fatal("สแกนซ้ำคนเดิมต้องไม่ยิง push อีก")
	case <-time.After(300 * time.Millisecond):
	}
}

// ไม่มี ParticipantID (แถวเก่า/ข้อมูลไม่ครบ) → ไม่มีใครให้ส่งถึง ต้องไม่สร้างแจ้งเตือน
// ลอยๆ ที่ audience_id ว่าง ซึ่งจะกลายเป็นประกาศที่ไม่มีเจ้าของ
func TestCheckinDoesNotNotifyWithoutParticipantID(t *testing.T) {
	svc, noti, _ := newStaffServiceUnderTest(&model.CheckinResult{
		AlreadyCheckedIn: false,
		ParticipantID:    "",
	})

	if _, err := svc.Checkin(context.Background(), "staff-1", model.StaffCheckinRequest{
		CheckpointID: ptr(7), Bib: ptr(123),
	}); err != nil {
		t.Fatalf("Checkin ไม่ควร error: %v", err)
	}

	select {
	case req := <-noti.created:
		t.Fatalf("ไม่มีผู้รับ ต้องไม่สร้างแจ้งเตือน แต่ได้ %+v", req)
	case <-time.After(300 * time.Millisecond):
	}
}

// เช็คอินพัง → ห้ามแจ้งเตือน (ไม่มีการเช็คอินเกิดขึ้นจริง)
func TestCheckinDoesNotNotifyWhenRepoFails(t *testing.T) {
	noti, push := newFakeNotifier(), newFakePusher()
	svc := NewWBWStaffService(&fakeStaffRepo{err: errors.New("db ล่ม")}, noti, push)

	if _, err := svc.Checkin(context.Background(), "staff-1", model.StaffCheckinRequest{
		CheckpointID: ptr(7), Bib: ptr(123),
	}); err == nil {
		t.Fatal("ต้องคืน error ต่อจาก repository")
	}

	select {
	case <-noti.created:
		t.Fatal("เช็คอินไม่สำเร็จ ต้องไม่มีแจ้งเตือน")
	case <-time.After(300 * time.Millisecond):
	}
}
