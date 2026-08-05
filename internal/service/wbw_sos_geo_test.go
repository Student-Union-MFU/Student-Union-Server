package service

import (
	"math"
	"testing"
)

// พิกัดจริงจาก db/migrations/000006_wbw_seed.up.sql — ถ้า seed เปลี่ยน เทสนี้ต้องเปลี่ยนตาม
var seededCheckpoints = []CheckpointPoint{
	{ID: 1, Name: "วิหารพระเจ้าล้านทอง", Lat: 20.04148, Lng: 99.89658},
	{ID: 2, Name: "สวนกุหลาบ", Lat: 20.04390, Lng: 99.89900},
	{ID: 5, Name: "จุดปลูก", Lat: 20.05300, Lng: 99.90080},
	{ID: 8, Name: "ฐาน Zero Waste", Lat: 20.05120, Lng: 99.89290},
}

func TestNearestCheckpointPicksTheClosestSeededBase(t *testing.T) {
	// ยืนห่างจากฐาน 2 ไปทางเหนือราว 30 เมตร
	got, dist := NearestCheckpoint(20.04417, 99.89900, seededCheckpoints)
	if got == nil || got.ID != 2 {
		t.Fatalf("อยากได้ฐาน 2 ได้ %v", got)
	}
	if dist < 20 || dist > 45 {
		t.Fatalf("ระยะควรราว 30 ม. ได้ %.1f", dist)
	}
}

func TestNearestCheckpointSeparatesAdjacentBases(t *testing.T) {
	// ฐาน 1 และ 2 ห่างกัน 300-500 ม. จุด (20.04200, 99.89700) ยืนห่างจาก 1 ราว 72 ม. จาก 2 ราว 297 ม. ต้องเลือก 1 ได้
	got, dist := NearestCheckpoint(20.04200, 99.89700, seededCheckpoints)
	if got == nil || got.ID != 1 {
		t.Fatalf("อยากได้ฐาน 1 ได้ %v", got)
	}
	if dist < 60 || dist > 85 {
		t.Fatalf("ระยะถึง 1 ควรราว 72 ม. ได้ %.1f", dist)
	}
}

func TestNearestCheckpointOnEmptyList(t *testing.T) {
	got, dist := NearestCheckpoint(20.045, 99.8825, nil)
	if got != nil || dist != 0 {
		t.Fatalf("ลิสต์ว่างต้องได้ (nil, 0) ได้ (%v, %v)", got, dist)
	}
}

func TestHaversineAgainstAKnownDistance(t *testing.T) {
	// หนึ่งองศาละติจูด ≈ 111.19 กม.
	d := haversineMeters(20.0, 99.0, 21.0, 99.0)
	if math.Abs(d-111195) > 500 {
		t.Fatalf("อยากได้ ~111195 ม. ได้ %.0f", d)
	}
}

func TestHaversineWithLongitudeDifference(t *testing.T) {
	// หนึ่งองศาลองจิจูดที่ละติจูด 20° ≈ 104.6 กม. (ไม่ใช่ 111.2 กม. ที่เส้นศูนย์สูตร)
	// ทดสอบนี้ยื่นให้ตรวจสอบการแบ่งระยะตามละติจูด — ถ้าลบปัจจัย cos(lat) ออก จะไม่ผ่าน
	d := haversineMeters(20.0, 99.0, 20.0, 100.0)
	if math.Abs(d-104500) > 3000 {
		t.Fatalf("หนึ่งองศาลองจิจูดที่ 20° ควรราว 104.5 กม. ได้ %.0f ม.", d)
	}
}
