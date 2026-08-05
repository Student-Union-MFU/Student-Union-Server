package service

import (
	"context"

	"su-server/internal/model"
	"su-server/internal/repository"
)

// progressRepo — เมธอดเดียวที่ service นี้ใช้จาก WBWCheckpointRepository ทั้งตัว แยกเป็น
// interface (แบบเดียวกับ sosRepo ใน wbw_sos_service.go) เพื่อให้เทสยิงของปลอมแทนได้
// โดยไม่ต้องต่อ DB จริง
type progressRepo interface {
	Progress(ctx context.Context, participantID string) (*model.CheckinProgress, error)
}

// ยืนยันตอนคอมไพล์ว่า repository ของจริงยังเข้ากับ interface นี้ได้ — ของปลอมในเทสยืนยันแทนไม่ได้
var _ progressRepo = (*repository.WBWCheckpointRepository)(nil)

// WBWProgressService — ความคืบหน้าเช็คอินของผู้เข้าร่วมที่เรียกเอง
type WBWProgressService struct {
	repo           progressRepo
	emergencyPhone string
}

// emergencyPhone มาจาก WBW_EMERGENCY_PHONE ที่ cmd/main.go อ่านครั้งเดียวแล้วส่งต่อให้ทั้ง
// service นี้และ WBWSOSService — ว่างได้ตอน dev แอปมีเบอร์ default ของตัวเองอยู่แล้ว
func NewWBWProgressService(repo progressRepo, emergencyPhone string) *WBWProgressService {
	return &WBWProgressService{repo: repo, emergencyPhone: emergencyPhone}
}

func (s *WBWProgressService) MyProgress(ctx context.Context, participantID string) (*model.CheckinProgress, error) {
	out, err := s.repo.Progress(ctx, participantID)
	if err != nil {
		return nil, err
	}
	// /me/progress ถูก poll ทุก 60 วิระหว่างเปิดแอป — ปุ่มโทรสำรองจึงมีเบอร์ที่ถูกต้องอยู่ใน
	// เครื่องตั้งแต่ก่อนเกิดเหตุ ไม่ใช่ได้มาหลังจากที่ส่ง SOS สำเร็จแล้ว (กรณีเดียวที่ไม่ต้องใช้
	// ปุ่มโทรสำรอง)
	out.EmergencyPhone = s.emergencyPhone
	return out, nil
}
