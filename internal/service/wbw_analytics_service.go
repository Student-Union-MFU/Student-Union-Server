package service

import (
	"context"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"
)

// WBWAnalyticsService — ชั้นบาง ๆ เหนือ repository
//
// ไม่มี cache โดยตั้งใจ แม้ endpoint นี้จะยิงหลาย query: ตัวเลขที่อยู่ในนั้นมี
// SOS ที่ยังไม่ปิดกับความคืบหน้าบนเส้นทางรวมอยู่ด้วย ซึ่งเป็นค่าที่คนเปิดดู
// "ตอนงานกำลังเดินอยู่" — เสิร์ฟของเก่าสิบวินาทีเพื่อประหยัด query บนตารางที่มี
// แถวหลักพันไม่คุ้มกับการที่ใครสักคนเห็นเคสฉุกเฉินช้าไปสิบวินาที
type WBWAnalyticsService struct {
	repo *repository.WBWAnalyticsRepository
}

func NewWBWAnalyticsService(repo *repository.WBWAnalyticsRepository) *WBWAnalyticsService {
	return &WBWAnalyticsService{repo: repo}
}

func (s *WBWAnalyticsService) Analytics(ctx context.Context) (*model.Analytics, error) {
	a, err := s.repo.Analytics(ctx)
	if err != nil {
		return nil, err
	}
	// เวลาที่ "ดึงข้อมูล" ไม่ใช่เวลาที่หน้าเว็บโหลด — ฝั่งเว็บเอาไปแสดงว่าตัวเลข
	// ที่เห็นสดแค่ไหน ซึ่งต่างกันจริงเมื่อแท็บถูกเปิดค้างไว้ทั้งวัน
	a.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return a, nil
}
