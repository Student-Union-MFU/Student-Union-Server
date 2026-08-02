package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
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
	noti *WBWNotificationService
	push *WBWPushService
}

func NewWBWStaffService(repo *repository.WBWStaffRepository, noti *WBWNotificationService, push *WBWPushService) *WBWStaffService {
	return &WBWStaffService{repo: repo, noti: noti, push: push}
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

	res, err := s.repo.Checkin(ctx, staffID, *req.CheckpointID, qr, req.Bib)
	if err != nil {
		return nil, err
	}

	// เช็คอินสำเร็จครั้งแรกเท่านั้นถึงเด้ง — สแกนซ้ำคนเดิมต้องไม่แจ้งเตือนซ้ำ
	//
	// ยิงทิ้งทั้งก้อน (บันทึกแถว notification + push) ด้วย context.WithoutCancel + goroutine
	// เหมือน SendChatPush ทุกประการ: เจ้าหน้าที่ยืนรอหน้าคิว ต้องไม่รอ round-trip DB
	// เพิ่มก่อนได้คำตอบ และ ctx ของ request ก็ถูกยกเลิกทันทีที่ตอบ response เสร็จอยู่แล้ว —
	// ตัว notifyFeedback เองไม่ต้องรู้เรื่อง goroutine เลย ให้ตรงนี้จัดการแทน
	if !res.AlreadyCheckedIn && res.ParticipantID != "" {
		detached := context.WithoutCancel(ctx)
		go s.notifyFeedback(detached, res.ParticipantID, *req.CheckpointID)
	}
	return res, nil
}

// notifyFeedback — แจ้งผู้เข้าร่วมว่าเช็คอินแล้วและขอความเห็นต่อฐาน
//
// เรียกจาก goroutine ที่แยกออกไปแล้วเสมอ (ดู Checkin) จึงไม่ต้องกังวลเรื่องความช้า
// ที่นี่อีกชั้น แต่ยังต้องทนต่อความล้มเหลวเอง: บันทึกแถวไม่สำเร็จก็แค่ log แล้วปล่อยผ่าน
// (การเช็คอินสำเร็จไปแล้วจริงๆ ตั้งแต่ก่อนเรียกฟังก์ชันนี้) · แอปยังเจอฐานที่ยังไม่ตอบได้
// จาก poll /me/progress อยู่ดี แจ้งเตือนเป็นทางลัด ไม่ใช่ทางเดียว
func (s *WBWStaffService) notifyFeedback(ctx context.Context, participantID string, checkpointID int) {
	name := s.repo.CheckpointName(ctx, checkpointID)
	title := "เช็คอิน " + name + " แล้ว"
	body := "แตะเพื่อให้คะแนนฐานนี้"
	typ, audience, level := "checkin_feedback", "user", "info"
	ref := strconv.Itoa(checkpointID)

	if _, err := s.noti.Create(ctx, model.NotificationRequest{
		Type: &typ, Title: title, Body: &body, Level: &level,
		Audience: &audience, AudienceID: &participantID, RefID: &ref,
	}, participantID); err != nil {
		slog.Error("สร้างแจ้งเตือนขอความเห็นไม่สำเร็จ", "err", err)
	}

	s.push.SendUserPush(ctx, participantID, title, body, map[string]string{
		"type":          "checkin_feedback",
		"checkpoint_id": ref,
	})
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
