package service

import (
	"context"
	"errors"
	"testing"

	"su-server/internal/model"
)

const testParticipantID = "11111111-1111-1111-1111-111111111111"

// fakeProgressRepo — ตัวปลอมของ progressRepo เทียบเท่า fakeSOSRepo ใน wbw_sos_service_test.go
// มีแค่เมธอดเดียวที่ service นี้ใช้จริง จึงไม่ต้องต่อ DB จริงเพื่อเทสชั้นนี้
type fakeProgressRepo struct {
	out *model.CheckinProgress
	err error

	gotParticipantID string
}

func (f *fakeProgressRepo) Progress(_ context.Context, participantID string) (*model.CheckinProgress, error) {
	f.gotParticipantID = participantID
	return f.out, f.err
}

// /me/progress ถูก poll ทุก 60 วิระหว่างเปิดแอป ปุ่มโทรสำรองจึงมีเบอร์ที่ถูกต้อง
// อยู่ในเครื่องตั้งแต่ก่อนเกิดเหตุ ไม่ใช่ได้มาหลังจากที่ส่ง SOS สำเร็จแล้ว
// (ซึ่งเป็นกรณีเดียวที่ไม่ต้องใช้ปุ่มโทร)
func TestProgressCarriesTheEmergencyPhoneSoTheAppCachesItBeforeItIsNeeded(t *testing.T) {
	repo := &fakeProgressRepo{out: &model.CheckinProgress{Total: 8, CheckedIn: []model.CheckinProgressItem{}}}
	svc := NewWBWProgressService(repo, "053-916-000")

	out, err := svc.MyProgress(context.Background(), testParticipantID)
	if err != nil {
		t.Fatal(err)
	}
	if out.EmergencyPhone != "053-916-000" {
		t.Fatalf("อยากได้เบอร์กลาง ได้ %q", out.EmergencyPhone)
	}
	if repo.gotParticipantID != testParticipantID {
		t.Fatalf("participantID ต้องถูกส่งต่อให้ repo ตรงๆ ได้ %q", repo.gotParticipantID)
	}
}

// ว่าง = dev ไม่ได้ตั้งเบอร์กลาง เป็นค่าที่ใช้ได้ปกติ ไม่ใช่ error และห้ามยัด placeholder ใดๆ
// แทน — แอป iOS มีเบอร์ default ของตัวเองอยู่แล้วเมื่อเจอค่าว่าง (ดูคอมเมนต์เรื่อง
// WBW_EMERGENCY_PHONE ที่จุดสร้าง service ใน cmd/main.go)
func TestProgressWithNoEmergencyPhoneConfiguredStaysEmpty(t *testing.T) {
	repo := &fakeProgressRepo{out: &model.CheckinProgress{Total: 8, CheckedIn: []model.CheckinProgressItem{}}}
	svc := NewWBWProgressService(repo, "")

	out, err := svc.MyProgress(context.Background(), testParticipantID)
	if err != nil {
		t.Fatal(err)
	}
	if out.EmergencyPhone != "" {
		t.Fatalf("เบอร์กลางว่างต้องคงว่าง ไม่ใช่ placeholder ได้ %q", out.EmergencyPhone)
	}
}

// repo ล้มเหลว → ต้องคืน error ไม่ใช่กลืนแล้วเดินหน้าไปเซ็ต EmergencyPhone ใส่ out ที่เป็น nil
// (จะ panic) — ตรึงลำดับ "เช็ค err ก่อนแตะ out" ไว้กันคนย้ายโค้ดแล้วพลาด
func TestProgressPropagatesRepositoryErrorInsteadOfPanicking(t *testing.T) {
	repo := &fakeProgressRepo{err: errors.New("db ล่ม")}
	svc := NewWBWProgressService(repo, "053-916-000")

	if _, err := svc.MyProgress(context.Background(), testParticipantID); err == nil {
		t.Fatal("repo error ต้องไม่ถูกกลืน")
	}
}
