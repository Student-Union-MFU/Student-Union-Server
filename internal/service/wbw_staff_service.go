package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"
)

var (
	ErrMissingCheckpoint = errors.New("missing checkpoint")
	ErrMissingIdentifier = errors.New("need qr_token or bib")
	ErrMissingToken      = errors.New("missing device token")
)

// notifyFeedbackTimeout — เพดานเวลาของ notifyFeedback ทั้งก้อน (CheckpointName +
// noti.Create; ส่วน push มี pushTimeout ของตัวเองแยกต่างหากอยู่แล้วใน SendUserPush)
// เผื่อเยอะพอสำหรับ query PK เดี่ยวๆ กับ insert แถวเดียวตอน DB ปกติ (หลัก ms) แต่สั้น
// พอที่ถ้า Postgres ค้างจริง goroutine จะคืน connection กลับ pool ภายในไม่กี่วินาที
// ไม่ใช่ยึดไว้ตลอดไปทีละ goroutine ต่อการสแกนหนึ่งครั้ง (วันงานจริงสแกนกันเป็นพันครั้ง)
const notifyFeedbackTimeout = 8 * time.Second

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
	// check_in ข้างบนนี้เขียนสำเร็จแบบ synchronous ไปแล้วก่อนจะยิง notifyFeedback ทิ้ง
	// /me/progress จึงเห็นฐานนี้ทันที ไม่ว่า notifyFeedback ข้างล่างจะช้าหรือพังแค่ไหน
	if !res.AlreadyCheckedIn && res.ParticipantID != "" {
		s.notifyFeedback(ctx, res.ParticipantID, *req.CheckpointID)
	}
	return res, nil
}

// notifyFeedback — แจ้งผู้เข้าร่วมว่าเช็คอินแล้วและขอความเห็นต่อฐาน · fire-and-forget
//
// รูปแบบเดียวกับ SendChatPush: context.WithoutCancel ตัด ctx ออกจาก request (ของเดิม
// ถูกยกเลิกทันทีที่ /staff/checkin ตอบ response เสร็จ) แล้วยิงใน goroutine แยก ครอบ
// ทั้งก้อนด้วย notifyFeedbackTimeout ให้ทั้ง CheckpointName และ noti.Create มีเพดานเวลา
// จริง — เดิมครอบแค่ SendUserPush (ซึ่งมี pushTimeout ของตัวเองอยู่แล้ว) สอง query DB
// ตรงนี้เลยไม่มีเพดานเลย ถ้า Postgres ค้าง goroutine นี้จะไม่มีวันจบและยึด connection
// ใน pool ไว้เงียบๆ (สแกนตอบ 200 ไปแล้ว ไม่มีใครเห็น error) · บันทึกแถวไม่สำเร็จก็แค่ log
// แล้วปล่อยผ่าน (การเช็คอินสำเร็จไปแล้วจริงๆ) แอปยังเจอฐานที่ยังไม่ตอบได้จาก poll
// /me/progress อยู่ดี แจ้งเตือนเป็นทางลัด ไม่ใช่ทางเดียว
func (s *WBWStaffService) notifyFeedback(ctx context.Context, participantID string, checkpointID int) {
	detached := context.WithoutCancel(ctx)
	go func() {
		c, cancel := context.WithTimeout(detached, notifyFeedbackTimeout)
		defer cancel()

		name := s.repo.CheckpointName(c, checkpointID)
		title := "เช็คอิน " + name + " แล้ว"
		body := "แตะเพื่อให้คะแนนฐานนี้"
		typ, audience, level := "checkin_feedback", "user", "info"
		ref := strconv.Itoa(checkpointID)

		if _, err := s.noti.Create(c, model.NotificationRequest{
			Type: &typ, Title: title, Body: &body, Level: &level,
			Audience: &audience, AudienceID: &participantID, RefID: &ref,
		}, participantID); err != nil {
			slog.Error("สร้างแจ้งเตือนขอความเห็นไม่สำเร็จ", "err", err)
		}

		s.push.SendUserPush(c, participantID, title, body, map[string]string{
			"type":          "checkin_feedback",
			"checkpoint_id": ref,
		})
	}()
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
