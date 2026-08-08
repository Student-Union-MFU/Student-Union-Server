package service

import "math"

// CheckpointPoint — ฐานหนึ่งจุดพร้อมพิกัด · แยกจาก model.StaffCheckpoint เพราะตัวนั้นไม่มี lat/lng
type CheckpointPoint struct {
	ID   int
	Name string
	Lat  float64
	Lng  float64
}

const earthRadiusMeters = 6371000.0

// haversineMeters — ระยะทางวงกลมใหญ่ระหว่างสองพิกัด หน่วยเมตร
//
// ทำไมไม่ใช้ PostGIS: เส้นทางทั้งงานมี 12 ฐาน กว้างราว 1.5 กม. การเปิด extension
// เพิ่มบน production เพื่อคำนวณ 12 บรรทัดในหน่วยความจำไม่คุ้มค่าดูแลเลย
func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const toRad = math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLng := (lng2 - lng1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusMeters * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// NearestCheckpoint — ฐานที่ใกล้พิกัดนี้ที่สุด พร้อมระยะเป็นเมตร
// ลิสต์ว่างคืน (nil, 0) — ผู้เรียกต้องรับมือกับกรณีไม่มีฐานให้เลือก
func NearestCheckpoint(lat, lng float64, points []CheckpointPoint) (*CheckpointPoint, float64) {
	var best *CheckpointPoint
	bestDist := math.Inf(1)
	for i := range points {
		d := haversineMeters(lat, lng, points[i].Lat, points[i].Lng)
		if d < bestDist {
			bestDist, best = d, &points[i]
		}
	}
	if best == nil {
		return nil, 0
	}
	return best, bestDist
}
