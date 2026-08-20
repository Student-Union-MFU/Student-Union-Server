package service

import (
	"context"

	"su-server/internal/model"
	"su-server/internal/repository"
)

// checkpointRepo — เมธอดเดียวที่ service นี้ใช้จาก WBWCheckpointRepository ทั้งตัว แยกเป็น
// interface (แบบเดียวกับ progressRepo ใน wbw_progress_service.go) เพื่อให้เทสยิงของปลอมแทนได้
// โดยไม่ต้องต่อ DB จริง
type checkpointRepo interface {
	ListForParticipant(ctx context.Context) ([]model.ParticipantCheckpoint, error)
}

// ยืนยันตอนคอมไพล์ว่า repository ของจริงยังเข้ากับ interface นี้ได้ — ของปลอมในเทสยืนยันแทนไม่ได้
var _ checkpointRepo = (*repository.WBWCheckpointRepository)(nil)

// WBWCheckpointService — รายการฐานทั้งงานสำหรับผู้เข้าร่วม
//
// แยกจาก WBWAdminService (ที่มี ListCheckpoints อยู่แล้ว) เพราะของแอดมินคืนรายชื่อเจ้าหน้าที่
// ประจำฐานมาด้วย ซึ่งเป็นข้อมูลที่ผู้เข้าร่วมไม่ควรได้ · และแยกจาก WBWProgressService เพราะ
// อันนั้นตอบเรื่อง "ความคืบหน้าของฉัน" ส่วนอันนี้เป็นข้อมูลของงานที่เหมือนกันทุกคน
type WBWCheckpointService struct {
	repo checkpointRepo
}

func NewWBWCheckpointService(repo checkpointRepo) *WBWCheckpointService {
	return &WBWCheckpointService{repo: repo}
}

func (s *WBWCheckpointService) List(ctx context.Context) ([]model.ParticipantCheckpoint, error) {
	return s.repo.ListForParticipant(ctx)
}
