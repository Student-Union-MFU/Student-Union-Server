package service

import (
	"context"
	"testing"

	"su-server/internal/model"
)

/*
เทสตรงนี้เรียก resolveBase (unexported) ตรงๆ แทนที่จะผ่าน Raise เหมือนเทสของ brief
เหตุผล: TestRaiseWithCoarseAccuracyDoesNotTrustTheBase ใน wbw_sos_service_test.go เช็คแค่
gotCheckpointID == nil ไม่เคยเช็คว่า loc_source ที่ทางแยกนี้คืนมาเป็นค่าอะไรจริงๆ — ทาง
"หยาบเกินไป" กับทาง "ไม่มีอะไรเลย" ต่างก็ให้ checkpointID = nil เหมือนกัน แยกออกจากกันได้
ที่ loc_source เท่านั้น (gps ตัวแรก vs none ตัวหลัง) และแอปฝั่ง client น่าจะใช้ค่านี้ตัดสินใจ
ว่าจะขึ้นข้อความว่า "มีตำแหน่งอยู่แต่ไม่แม่น" หรือ "ไม่มีตำแหน่งเลย" จึงต้องแยกให้ถูก
*/
func TestResolveBaseWithCoarseAccuracyReturnsGPSNotNone(t *testing.T) {
	lat, lng, acc := 20.04395, 99.89905, 500.0
	last := 5
	f := &fakeSOSRepo{checkpoints: seed, lastCheckin: &last}
	s := newSOSService(f)

	cp, src := s.resolveBase(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z", Lat: &lat, Lng: &lng, AccuracyM: &acc,
	})

	if cp != nil {
		t.Fatalf("ความแม่นแย่กว่า 200 ม. ห้ามผูกฐาน ได้ %v", *cp)
	}
	if src == nil || *src != "gps" {
		t.Fatalf("มีพิกัดจริง (แค่หยาบ) loc_source ต้องเป็น gps ไม่ใช่ %v — ไม่งั้นแอปแยกไม่ออกจาก 'ไม่มีตำแหน่งเลย'", src)
	}
}

// กรณีพิกัดหยาบ "และ" มีฐานล่าสุดที่เช็คอินอยู่ด้วย — โค้ดต้อง return ตรงจุด accuracy
// เช็คก่อนถึงจะไปแตะ LastCheckinCheckpoint เลย ไม่ใช่ไหลลงไปถอยเข้าฐานเช็คอินแทน
// (ถ้าใครมาแก้แล้วเผลอให้มันไหลผ่าน จะได้ฐานที่อาจผิดจากที่คนอยู่จริงตอนนี้แบบเงียบๆ)
func TestResolveBaseWithCoarseAccuracyDoesNotFallBackToLastCheckin(t *testing.T) {
	lat, lng, acc := 20.04395, 99.89905, 500.0
	last := 5
	f := &fakeSOSRepo{checkpoints: seed, lastCheckin: &last}
	s := newSOSService(f)

	cp, src := s.resolveBase(context.Background(), "u1", model.SOSRequest{
		ClientID: "x", DeviceTime: "2026-08-06T10:00:00Z", Lat: &lat, Lng: &lng, AccuracyM: &acc,
	})

	if cp != nil {
		t.Fatalf("ห้ามถอยไปใช้ฐานเช็คอิน (5) ทั้งที่มี GPS หยาบอยู่ ได้ %v", *cp)
	}
	if src == nil || *src != "gps" {
		t.Fatalf("loc_source ต้องยังเป็น gps ไม่ใช่ last_checkin ได้ %v", src)
	}
}
