package service

import (
	"context"
	"testing"

	"su-server/internal/model"
	"su-server/internal/repository"
)

type fakeSOSRepo struct {
	checkpoints []repository.CheckpointGeo
	lastCheckin *int

	gotCheckpointID *int
	gotLocSource    *string
	raised          *model.SOSCase
	created         bool

	pushAllowed bool
	marked      bool
}

func (f *fakeSOSRepo) Checkpoints(context.Context) ([]repository.CheckpointGeo, error) {
	return f.checkpoints, nil
}
func (f *fakeSOSRepo) LastCheckinCheckpoint(context.Context, string) (*int, error) {
	return f.lastCheckin, nil
}
func (f *fakeSOSRepo) Raise(_ context.Context, _ string, _ model.SOSRequest, cp *int, src *string) (*model.SOSCase, bool, error) {
	f.gotCheckpointID, f.gotLocSource = cp, src
	return f.raised, f.created, nil
}
func (f *fakeSOSRepo) PushAllowed(context.Context, int64) (bool, error) { return f.pushAllowed, nil }
func (f *fakeSOSRepo) MarkPushed(context.Context, int64) error          { f.marked = true; return nil }

// เมธอดที่เหลือไม่ถูกเรียกในเทสชุดนี้ — คืนค่าว่างพอ
func (f *fakeSOSRepo) Get(context.Context, int64) (*model.SOSCase, error) { return f.raised, nil }
func (f *fakeSOSRepo) GetForViewer(context.Context, string, int64) (*model.SOSCase, error) {
	return f.raised, nil
}
func (f *fakeSOSRepo) Cancel(context.Context, string, int64) error          { return nil }
func (f *fakeSOSRepo) Ack(context.Context, int64, string) error             { return nil }
func (f *fakeSOSRepo) Resolve(context.Context, int64, string, string) error { return nil }
func (f *fakeSOSRepo) StaffFeed(context.Context, string, string, string) ([]model.SOSStaffCase, error) {
	return nil, nil
}
func (f *fakeSOSRepo) CanStaffSee(context.Context, string, string, int64) (bool, error) {
	return true, nil
}
func (f *fakeSOSRepo) PushAudience(context.Context, int64) (*repository.SOSAudience, error) {
	return &repository.SOSAudience{}, nil
}
func (f *fakeSOSRepo) ActiveFor(context.Context, string) (*model.SOSCase, error) { return nil, nil }

var seed = []repository.CheckpointGeo{
	{ID: 1, Name: "วิหารพระเจ้าล้านทอง", Lat: 20.04148, Lng: 99.89658},
	{ID: 2, Name: "สวนกุหลาบ", Lat: 20.04390, Lng: 99.89900},
}

func newSOSService(f *fakeSOSRepo) *WBWSOSService {
	return NewWBWSOSService(f, NewSOSEvents(nil, nil), nil, nil, "053-916-000")
}

func TestRaiseWithGPSResolvesTheNearestBase(t *testing.T) {
	lat, lng := 20.04395, 99.89905
	f := &fakeSOSRepo{checkpoints: seed, raised: &model.SOSCase{ID: 1}, created: true}
	_, _, err := newSOSService(f).Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z", Lat: &lat, Lng: &lng,
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.gotCheckpointID == nil || *f.gotCheckpointID != 2 {
		t.Fatalf("ควรได้ฐาน 2 ได้ %v", f.gotCheckpointID)
	}
	if f.gotLocSource == nil || *f.gotLocSource != "gps" {
		t.Fatalf("loc_source ควรเป็น gps ได้ %v", f.gotLocSource)
	}
}

func TestRaiseWithoutGPSFallsBackToTheLastCheckin(t *testing.T) {
	last := 5
	f := &fakeSOSRepo{checkpoints: seed, lastCheckin: &last, raised: &model.SOSCase{ID: 1}, created: true}
	if _, _, err := newSOSService(f).Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if f.gotCheckpointID == nil || *f.gotCheckpointID != 5 {
		t.Fatalf("ควรถอยไปฐานล่าสุดที่เช็คอิน (5) ได้ %v", f.gotCheckpointID)
	}
	if f.gotLocSource == nil || *f.gotLocSource != "last_checkin" {
		t.Fatalf("loc_source ควรเป็น last_checkin ได้ %v", f.gotLocSource)
	}
}

func TestRaiseWithNothingAtAllIsStillARealCase(t *testing.T) {
	f := &fakeSOSRepo{checkpoints: seed, raised: &model.SOSCase{ID: 1}, created: true}
	c, _, err := newSOSService(f).Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z",
	})
	if err != nil || c == nil {
		t.Fatalf("ไม่มีพิกัดและไม่เคยเช็คอิน ต้องยังเปิดเคสได้: %v", err)
	}
	if f.gotCheckpointID != nil {
		t.Fatal("ไม่ควรเดาฐาน")
	}
	if f.gotLocSource == nil || *f.gotLocSource != "none" {
		t.Fatalf("loc_source ควรเป็น none ได้ %v", f.gotLocSource)
	}
}

func TestRaiseWithCoarseAccuracyDoesNotTrustTheBase(t *testing.T) {
	lat, lng, acc := 20.04395, 99.89905, 500.0
	f := &fakeSOSRepo{checkpoints: seed, raised: &model.SOSCase{ID: 1}, created: true}
	if _, _, err := newSOSService(f).Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z", Lat: &lat, Lng: &lng, AccuracyM: &acc,
	}); err != nil {
		t.Fatal(err)
	}
	if f.gotCheckpointID != nil {
		t.Fatal("ความแม่นแย่กว่า 200 ม. ห้ามผูกเคสกับฐานใดฐานหนึ่ง")
	}
}

func TestRaiseRejectsAMissingClientID(t *testing.T) {
	f := &fakeSOSRepo{checkpoints: seed}
	if _, _, err := newSOSService(f).Raise(context.Background(), "u1", model.SOSRequest{
		ClientID: "  ", DeviceTime: "2026-08-06T10:00:00Z",
	}); err != ErrSOSMissingClientID {
		t.Fatalf("อยากได้ ErrSOSMissingClientID ได้ %v", err)
	}
}

func TestResolveRejectsAnUnknownReason(t *testing.T) {
	f := &fakeSOSRepo{}
	if err := newSOSService(f).Resolve(context.Background(), "s1", "staff", 1, "เพราะอยาก"); err != ErrSOSBadReason {
		t.Fatalf("อยากได้ ErrSOSBadReason ได้ %v", err)
	}
}

func TestResolveAcceptsEveryReasonInTheSpec(t *testing.T) {
	for _, reason := range []string{"helped", "false_alarm", "unreachable", "canceled_by_user"} {
		if err := newSOSService(&fakeSOSRepo{}).Resolve(context.Background(), "s1", "staff", 1, reason); err != nil {
			t.Fatalf("%s ควรผ่าน ได้ %v", reason, err)
		}
	}
}

func (f *fakeSOSRepo) SetSeverity(context.Context, int64, string, string) error { return nil }

func (f *fakeSOSRepo) Escalate(context.Context, int64, string) error { return nil }
