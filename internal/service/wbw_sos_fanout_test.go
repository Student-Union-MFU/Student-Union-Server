package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"
)

/*
เทสชุดนี้ปิดช่องที่เทสของ brief (wbw_sos_service_test.go) ไม่ครอบคลุม: เทสชุดนั้นเรียก
newSOSService(f) ด้วย push = nil, noti = nil เสมอ ทำให้ announce()/announceClosed() คืน
ทันทีที่เจอ `if s.push == nil { return }` — เส้นทาง PushAudience, ประตู 60 วิ (PushAllowed),
"กลุ่มได้แค่เปิด/ปิด" และ MarkPushed ไม่เคยถูกเดินจริงเลยสักครั้ง

จุดนี้คือกฎที่พังแล้วสเปกบอกไว้ตรงๆ ว่าอันตรายที่สุด ("กลุ่ม 50 คนต้องได้ push แค่สอง
ครั้งต่อเคส... พลาดจุดนี้คือสแปมโทรศัพท์ 50 เครื่องกลางเหตุฉุกเฉิน") จึงต้องมีเทสที่ผ่าน push/noti
ปลอมจริงๆ แล้ว "รอ" ผลจาก goroutine (เหมือน TestCheckinNotifiesOnFirstCheckin ใน
wbw_staff_service_test.go) ไม่ใช่แค่เชื่อจากการอ่านโค้ด
*/

// fanoutFakeRepo — ตัวปลอมแยกจาก fakeSOSRepo ของ brief โดยตั้งใจ: PushAudience/MarkPushed
// ของตัวนั้น hardcode ค่าว่างและไม่ปลอดภัยเมื่ออ่าน/เขียนข้าม goroutine (announce ทำงานใน
// goroutine แยกเสมอ) ตัวนี้ให้ตั้งค่า audience/pushAllowed ได้ และรายงาน MarkPushed ผ่าน channel
type fanoutFakeRepo struct {
	raised      *model.SOSCase
	created     bool
	getResult   *model.SOSCase
	audience    *repository.SOSAudience
	pushAllowed bool
	markedCh    chan struct{}
}

func (f *fanoutFakeRepo) Checkpoints(context.Context) ([]repository.CheckpointGeo, error) {
	return nil, nil
}
func (f *fanoutFakeRepo) LastCheckinCheckpoint(context.Context, string) (*int, error) {
	return nil, nil
}
func (f *fanoutFakeRepo) Raise(context.Context, string, model.SOSRequest, *int, *string) (*model.SOSCase, bool, error) {
	return f.raised, f.created, nil
}
func (f *fanoutFakeRepo) Get(context.Context, int64) (*model.SOSCase, error) { return f.getResult, nil }
func (f *fanoutFakeRepo) GetForViewer(context.Context, string, int64) (*model.SOSCase, error) {
	return f.getResult, nil
}
func (f *fanoutFakeRepo) ActiveFor(context.Context, string) (*model.SOSCase, error) { return nil, nil }
func (f *fanoutFakeRepo) Cancel(context.Context, string, int64) error               { return nil }
func (f *fanoutFakeRepo) Ack(context.Context, int64, string) error                  { return nil }
func (f *fanoutFakeRepo) Resolve(context.Context, int64, string, string) error      { return nil }
func (f *fanoutFakeRepo) StaffFeed(context.Context, string, string, string) ([]model.SOSStaffCase, error) {
	return nil, nil
}
func (f *fanoutFakeRepo) CanStaffSee(context.Context, string, string, int64) (bool, error) {
	return true, nil
}
func (f *fanoutFakeRepo) PushAudience(context.Context, int64) (*repository.SOSAudience, error) {
	return f.audience, nil
}
func (f *fanoutFakeRepo) PushAllowed(context.Context, int64) (bool, error) { return f.pushAllowed, nil }
func (f *fanoutFakeRepo) MarkPushed(context.Context, int64) error {
	select {
	case f.markedCh <- struct{}{}:
	default:
	}
	return nil
}

type sentPush struct {
	tokens []string
	title  string
	body   string
}

type fakeSOSPush struct{ sent chan sentPush }

func newFakeSOSPush() *fakeSOSPush { return &fakeSOSPush{sent: make(chan sentPush, 8)} }

func (f *fakeSOSPush) SendToTokens(_ context.Context, tokens []string, title, body string, _ map[string]string) {
	f.sent <- sentPush{tokens: tokens, title: title, body: body}
}

type fakeSOSNoti struct {
	created chan model.NotificationRequest
}

func newFakeSOSNoti() *fakeSOSNoti {
	return &fakeSOSNoti{created: make(chan model.NotificationRequest, 8)}
}

func (f *fakeSOSNoti) Create(_ context.Context, req model.NotificationRequest, _ string) (*model.Notification, error) {
	f.created <- req
	return &model.Notification{}, nil
}

func drainOne(t *testing.T, ch chan sentPush) sentPush {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("ไม่มี push เข้ามาในเวลาที่กำหนด")
		return sentPush{}
	}
}

func assertNoMorePush(t *testing.T, ch chan sentPush) {
	t.Helper()
	select {
	case p := <-ch:
		t.Fatalf("ไม่ควรมี push เพิ่มแล้ว แต่ได้ %+v", p)
	case <-time.After(300 * time.Millisecond):
	}
}

func tokenKey(p sentPush) string { return fmt.Sprintf("%v", p.tokens) }

// เคสเปิดใหม่ (created = true) ต้องยิงครบสามทาง: เจ้าหน้าที่ประจำฐาน, ทีมกลาง, กลุ่มเพื่อน
// (ครั้งแรกของสองครั้งที่กลุ่มมีสิทธิ์ได้) และต้องจดเวลา push ไว้ด้วย (MarkPushed)
func TestAnnounceOnCreatePushesStaffCentralGroupAndMarksPushed(t *testing.T) {
	push, noti := newFakeSOSPush(), newFakeSOSNoti()
	// GroupID ต้องมีค่าจริง — notifyGroup ใช้เป็น notification.audience_id · เคสที่ไม่มีกลุ่มก็ไม่มี
	// ใครให้แจ้ง จึงข้ามการสร้างแถวไปเลยโดยตั้งใจ (ดูคอมเมนต์ที่ notifyGroup)
	group := 7
	repo := &fanoutFakeRepo{
		raised:  &model.SOSCase{ID: 42, GroupID: &group},
		created: true,
		audience: &repository.SOSAudience{
			StaffTokens: []string{"staff-1"}, CentralTokens: []string{"central-1"}, GroupTokens: []string{"group-1"},
		},
		markedCh: make(chan struct{}, 1),
	}
	svc := NewWBWSOSService(repo, NewSOSEvents(nil, nil), push, noti, "053-916-000")

	if _, _, err := svc.Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		got[tokenKey(drainOne(t, push.sent))] = true
	}
	if !got["[staff-1]"] || !got["[central-1]"] || !got["[group-1]"] {
		t.Fatalf("เคสเปิดใหม่ต้องยิงครบสามกลุ่ม ได้ %+v", got)
	}
	assertNoMorePush(t, push.sent)

	// ค่าที่ส่งเข้า Create ต้องตรงกับ "สิ่งที่ฐานข้อมูลยอมรับ" ไม่ใช่แค่ว่ามีการเรียกเกิดขึ้น —
	// การพิสูจน์จริงว่าแถวเกิดขึ้นได้อยู่ที่ TestRaiseNotifyGroupWritesARowTheGroupCanActuallySee
	// (แตะ Postgres จริง) ตรงนี้แค่ค้ำไม่ให้ค่าเพี้ยนกลับไปโดยไม่มีใครเห็น
	select {
	case req := <-noti.created:
		if req.Level == nil || *req.Level != "emergency" {
			t.Fatalf("level ต้องเป็น emergency (สมาชิกของ enum noti_level) ได้ %v", req.Level)
		}
		if req.AudienceID == nil || *req.AudienceID != "7" {
			t.Fatalf("audience_id ต้องเป็นกลุ่มของคนกด ได้ %v", req.AudienceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("เคสเปิดใหม่ต้องสร้างแถวแจ้งเตือนให้กลุ่มด้วย ไม่ใช่แค่ push")
	}

	select {
	case <-repo.markedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("ต้องจดเวลา push ไว้ (MarkPushed) ไม่งั้นประตู 60 วิของ 'ย้ำ' ครั้งถัดไปพัง")
	}
}

// ย้ำ (created = false) ที่ยังไม่ครบ 60 วิจากครั้งก่อน (PushAllowed = false) ต้องไม่ยิงอะไร
// เลยสักทาง — ไม่ใช่แค่ "ไม่ยิงกลุ่ม" แต่ห้ามยิงแม้แต่เจ้าหน้าที่/ทีมกลาง และห้ามจดเวลาใหม่
func TestAnnounceOnBumpWithoutPushAllowedSendsNoPushAtAll(t *testing.T) {
	push, noti := newFakeSOSPush(), newFakeSOSNoti()
	repo := &fanoutFakeRepo{
		raised: &model.SOSCase{ID: 42}, created: false, pushAllowed: false,
		audience: &repository.SOSAudience{
			StaffTokens: []string{"staff-1"}, CentralTokens: []string{"central-1"}, GroupTokens: []string{"group-1"},
		},
		markedCh: make(chan struct{}, 1),
	}
	svc := NewWBWSOSService(repo, NewSOSEvents(nil, nil), push, noti, "053-916-000")

	if _, _, err := svc.Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	assertNoMorePush(t, push.sent)
	select {
	case req := <-noti.created:
		t.Fatalf("ยังไม่ครบ 60 วิ ต้องไม่มีแจ้งเตือนกลุ่ม ได้ %+v", req)
	case <-repo.markedCh:
		t.Fatal("ยังไม่ครบ 60 วิ (ห้ามยิง) ต้องไม่จดเวลา push ใหม่ทับของเดิม")
	case <-time.After(300 * time.Millisecond):
	}
}

// ย้ำที่ผ่าน 60 วิแล้ว (PushAllowed = true) ต้องยิงซ้ำหาเจ้าหน้าที่กับทีมกลางเท่านั้น
// ห้ามยิงเข้ากลุ่มเพื่อน — กลุ่มมีโควตาแค่เปิดกับปิด ไม่รวมการย้ำ
func TestAnnounceOnBumpWithPushAllowedRepushesStaffAndCentralOnly(t *testing.T) {
	push, noti := newFakeSOSPush(), newFakeSOSNoti()
	repo := &fanoutFakeRepo{
		raised: &model.SOSCase{ID: 42}, created: false, pushAllowed: true,
		audience: &repository.SOSAudience{
			StaffTokens: []string{"staff-1"}, CentralTokens: []string{"central-1"}, GroupTokens: []string{"group-1"},
		},
		markedCh: make(chan struct{}, 1),
	}
	svc := NewWBWSOSService(repo, NewSOSEvents(nil, nil), push, noti, "053-916-000")

	if _, _, err := svc.Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		got[tokenKey(drainOne(t, push.sent))] = true
	}
	if !got["[staff-1]"] || !got["[central-1]"] {
		t.Fatalf("ย้ำที่ผ่าน 60 วิ ต้องยิงซ้ำหาเจ้าหน้าที่และทีมกลาง ได้ %+v", got)
	}
	if got["[group-1]"] {
		t.Fatal("ย้ำต้องไม่ยิงเข้ากลุ่มเพื่อน — กลุ่มได้แค่เปิดกับปิดเท่านั้น")
	}
	assertNoMorePush(t, push.sent)

	select {
	case <-noti.created:
		t.Fatal("ย้ำต้องไม่สร้างแจ้งเตือนกลุ่มใหม่")
	default:
	}

	select {
	case <-repo.markedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("ย้ำที่ยิง push จริงต้องจดเวลาไว้ด้วย ไม่งั้นย้ำครั้งถัดมาจะยิงถี่กว่า 60 วิ")
	}
}

// ปิดเคสสำเร็จ (Resolve) ต้องยิงกลุ่มครั้งเดียว ด้วยข้อความกลางๆ ไม่บอกเหตุผลจริง
// และต้องไม่ยิงหาเจ้าหน้าที่/ทีมกลางซ้ำ (พวกเขาเห็นจาก long-poll ของ staff feed แทน)
func TestResolvePushesGroupExactlyOnceWithClosedMessage(t *testing.T) {
	push, noti := newFakeSOSPush(), newFakeSOSNoti()
	repo := &fanoutFakeRepo{
		getResult: &model.SOSCase{ID: 42, ResolveReason: ptr("helped")},
		audience: &repository.SOSAudience{
			StaffTokens: []string{"staff-1"}, CentralTokens: []string{"central-1"}, GroupTokens: []string{"group-1"},
		},
		markedCh: make(chan struct{}, 1),
	}
	svc := NewWBWSOSService(repo, NewSOSEvents(nil, nil), push, noti, "053-916-000")

	if err := svc.Resolve(context.Background(), "staff-9", "staff", 42, "helped"); err != nil {
		t.Fatal(err)
	}

	p := drainOne(t, push.sent)
	if tokenKey(p) != "[group-1]" {
		t.Fatalf("ปิดเคสต้องยิงหากลุ่มเท่านั้น ได้ %+v", p)
	}
	if p.body != "เจ้าหน้าที่ดูแลเรียบร้อยแล้ว" {
		t.Fatalf("ข้อความปิดเคสไม่ตรง (ต้องไม่รั่วเหตุผลจริงออกไปทั้งกลุ่ม) ได้ %q", p.body)
	}
	assertNoMorePush(t, push.sent)
}

// ยกเลิกเอง (Cancel) ก็เป็น "ปิด" อีกแบบหนึ่ง — ใช้คำว่า "ยกเลิกแล้ว" แทน
func TestCancelPushesGroupWithCanceledWording(t *testing.T) {
	push, noti := newFakeSOSPush(), newFakeSOSNoti()
	repo := &fanoutFakeRepo{
		getResult: &model.SOSCase{ID: 42, ResolveReason: ptr("canceled_by_user")},
		audience: &repository.SOSAudience{
			StaffTokens: []string{"staff-1"}, CentralTokens: []string{"central-1"}, GroupTokens: []string{"group-1"},
		},
		markedCh: make(chan struct{}, 1),
	}
	svc := NewWBWSOSService(repo, NewSOSEvents(nil, nil), push, noti, "053-916-000")

	if err := svc.Cancel(context.Background(), "u1", 42); err != nil {
		t.Fatal(err)
	}

	p := drainOne(t, push.sent)
	if p.body != "ยกเลิกแล้ว" {
		t.Fatalf("ยกเลิกเองต้องขึ้นข้อความ 'ยกเลิกแล้ว' ได้ %q", p.body)
	}
	assertNoMorePush(t, push.sent)
}
