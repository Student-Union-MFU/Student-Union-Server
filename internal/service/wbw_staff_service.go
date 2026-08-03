package service

import (
	"context"
	"errors"
	"strings"

	"su-server/internal/model"
	"su-server/internal/repository"
)

var (
	ErrMissingCheckpoint = errors.New("missing checkpoint")
	ErrMissingIdentifier = errors.New("need qr_token or bib")
	ErrMissingToken      = errors.New("missing device token")
)

type WBWStaffService struct {
	repo *repository.WBWStaffRepository
}

func NewWBWStaffService(repo *repository.WBWStaffRepository) *WBWStaffService {
	return &WBWStaffService{repo: repo}
}

func (s *WBWStaffService) Checkpoints(ctx context.Context, userID, role string) ([]model.StaffCheckpoint, error) {
	return s.repo.Checkpoints(ctx, userID, role)
}

// Checkin — ต้องมีฐาน และต้องระบุคนด้วย qr_token หรือ bib อย่างน้อยหนึ่งอย่าง
// qr_token มาก่อน bib เมื่อส่งมาทั้งคู่ (สแกน QR แม่นกว่าพิมพ์เลขมือ)
func (s *WBWStaffService) Checkin(ctx context.Context, staffID string, req model.StaffCheckinRequest) (*model.CheckinResult, error) {
	if req.CheckpointID == nil {
		return nil, ErrMissingCheckpoint
	}

	var qr *string
	if req.QRToken != nil {
		if t := strings.TrimSpace(*req.QRToken); t != "" {
			qr = &t
		}
	}
	if qr == nil && req.Bib == nil {
		return nil, ErrMissingIdentifier
	}
	return s.repo.Checkin(ctx, staffID, *req.CheckpointID, qr, req.Bib)
}

/* ---------- device token ---------- */

type WBWDeviceService struct {
	repo *repository.WBWDeviceRepository
}

func NewWBWDeviceService(repo *repository.WBWDeviceRepository) *WBWDeviceService {
	return &WBWDeviceService{repo: repo}
}

func (s *WBWDeviceService) Register(ctx context.Context, userID string, req model.DeviceRegisterRequest) error {
	if strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.Platform) == "" {
		return ErrMissingToken
	}
	return s.repo.Register(ctx, userID, req.Token, req.Platform)
}

// Unregister — ไม่มี token ส่งมาก็ถือว่าสำเร็จ (logout ตอนยังไม่เคยลงทะเบียน push)
func (s *WBWDeviceService) Unregister(ctx context.Context, userID string, req model.DeviceUnregisterRequest) error {
	if strings.TrimSpace(req.Token) == "" {
		return nil
	}
	return s.repo.Unregister(ctx, userID, req.Token)
}
