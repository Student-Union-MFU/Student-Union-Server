package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"
)

var (
	ErrNotGroupMember  = errors.New("not a group member")
	ErrEmptyBody       = errors.New("empty message body")
	ErrBodyTooLong     = errors.New("message body too long")
	ErrMissingClientID = errors.New("missing client_id")
	ErrBadLastReadID   = errors.New("bad last_read_id")
	ErrClientIDTaken   = errors.New("client_id already used by another message")
	// ErrBadClientID — client_id ไม่ใช่รูป UUID · PG ตอบ 22P02 (invalid text representation)
	// เป็น input ผิด ไม่ใช่ความผิดพลาดของ server
	ErrBadClientID = errors.New("bad client_id")
)

const (
	// ยาวสุดต่อข้อความ — ตรงกับฝั่ง Node เดิม
	maxBodyLen = 2000
	// long-poll ค้างได้นานสุด 25 วิ · สั้นกว่า timeout ปกติของ proxy/load balancer (30-60 วิ)
	// เพื่อให้ฝั่ง server เป็นคนปิดรอบเอง ไม่ใช่โดน proxy ตัดกลางคัน
	maxWaitSeconds = 25
	// จำนวนข้อความสูงสุดต่อรอบ sync
	syncLimit = 50
)

type WBWChatService struct {
	repo   *repository.WBWChatRepository
	events *ChatEvents
	push   ChatPusher
}

// ChatPusher ส่ง push ของข้อความแชท · แยกเป็น interface เพื่อให้ปิด push ได้
// (ยังไม่ตั้ง Firebase credential ก็ให้ทั้งระบบทำงานต่อได้ แค่ไม่มี push)
type ChatPusher interface {
	SendChatPush(ctx context.Context, msg model.Message)
}

func NewWBWChatService(repo *repository.WBWChatRepository, events *ChatEvents, push ChatPusher) *WBWChatService {
	return &WBWChatService{repo: repo, events: events, push: push}
}

func (s *WBWChatService) guard(ctx context.Context, userID string, groupID int) error {
	ok, err := s.repo.IsMember(ctx, userID, groupID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotGroupMember
	}
	return nil
}

// Sync — ข้อความ + สถานะอ่าน ในคำขอเดียว · wait > 0 = long-poll
//
// หมดเวลาแล้วยังไม่มีข้อความใหม่ = ตอบ 200 พร้อม messages ว่าง ไม่ใช่ error:
// client จะยิงรอบใหม่ทันที ซึ่งเป็นพฤติกรรมปกติของ long-poll
func (s *WBWChatService) Sync(ctx context.Context, userID string, groupID int, afterID int64, waitSeconds int) (*model.ChatSyncResponse, error) {
	if err := s.guard(ctx, userID, groupID); err != nil {
		return nil, err
	}

	sinceID, err := s.repo.SinceID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}

	messages, err := s.repo.MessagesAfter(ctx, groupID, afterID, sinceID, syncLimit)
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 && waitSeconds > 0 {
		// ค้างไว้จนมีเหตุการณ์ของกลุ่มนี้ หรือหมดเวลา หรือ client หลุด
		// exceptActor = ตัวเอง: การกระทำของเราเองไม่ต้องปลุก long-poll ของเราเอง
		if s.events.WaitForGroup(ctx, groupID, time.Duration(waitSeconds)*time.Second, userID) {
			messages, err = s.repo.MessagesAfter(ctx, groupID, afterID, sinceID, syncLimit)
			if err != nil {
				return nil, err
			}
		}
		// client หลุดกลางคัน — เลิกทำงานต่อ ไม่มีใครรอรับคำตอบแล้ว
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	// โหลด cursor หลังตื่น เพื่อให้สถานะ "อ่านแล้ว" สดที่สุด
	memberCount, err := s.repo.MemberCount(ctx, groupID)
	if err != nil {
		return nil, err
	}
	cursors, err := s.repo.Cursors(ctx, groupID, userID)
	if err != nil {
		return nil, err
	}

	return &model.ChatSyncResponse{
		SinceID:     sinceID,
		MemberCount: memberCount,
		Messages:    messages,
		Cursors:     cursors,
	}, nil
}

// MarkRead — ยิงซ้ำค่าเดิมได้ ใช้เป็น heartbeat "กำลังเปิดจอแชทอยู่" (push จะข้ามคนนี้)
func (s *WBWChatService) MarkRead(ctx context.Context, userID string, groupID int, req model.ChatReadRequest) error {
	if req.LastReadID == nil || *req.LastReadID < 0 {
		return ErrBadLastReadID
	}
	if err := s.guard(ctx, userID, groupID); err != nil {
		return err
	}
	if err := s.repo.MarkRead(ctx, userID, groupID, *req.LastReadID); err != nil {
		return err
	}
	// ปลุกคนอื่นให้เห็นสถานะ "อ่านแล้ว" สดทันที · ข้ามตัวเอง
	s.events.NotifyGroup(ctx, groupID, userID)
	return nil
}

// Messages — poll แบบเดิม (ไม่ long-poll) · ไม่ส่ง after = N ข้อความล่าสุด
func (s *WBWChatService) Messages(ctx context.Context, userID string, groupID int, afterID *int64, limit int) ([]model.Message, error) {
	if err := s.guard(ctx, userID, groupID); err != nil {
		return nil, err
	}
	sinceID, err := s.repo.SinceID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	if afterID != nil {
		return s.repo.MessagesAfter(ctx, groupID, *afterID, sinceID, limit)
	}
	return s.repo.MessagesLatest(ctx, groupID, sinceID, limit)
}

// Send — ส่งข้อความ · idempotent ด้วย client_id
func (s *WBWChatService) Send(ctx context.Context, userID string, groupID int, req model.SendMessageRequest) (*model.Message, error) {
	if strings.TrimSpace(req.ClientID) == "" {
		return nil, ErrMissingClientID
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" {
		return nil, ErrEmptyBody
	}
	if len([]rune(req.Body)) > maxBodyLen {
		return nil, ErrBodyTooLong
	}
	if err := s.guard(ctx, userID, groupID); err != nil {
		return nil, err
	}

	deviceTime := time.Now().UTC().Format(time.RFC3339Nano)
	if req.DeviceTime != nil && *req.DeviceTime != "" {
		deviceTime = *req.DeviceTime
	}

	msg, err := s.repo.InsertMessage(ctx, groupID, userID, req, deviceTime)
	if err != nil {
		if repository.IsPGCode(err, "22P02") {
			return nil, ErrBadClientID
		}
		return nil, err
	}
	if msg == nil {
		// client_id นี้ถูกใช้ไปแล้วโดยคนอื่นหรือกลุ่มอื่น — ไม่ใช่การ retry ของเจ้าของ
		return nil, ErrClientIDTaken
	}

	// actor ว่าง = ปลุกทุกคนรวมถึงเครื่องอื่นของคนส่งเอง (เขาอาจเปิดแอปอยู่หลายเครื่อง)
	s.events.NotifyGroup(ctx, groupID, "")
	if s.push != nil {
		s.push.SendChatPush(ctx, *msg)
	}
	return msg, nil
}

// ClampWait บีบค่า wait ให้อยู่ในช่วงที่รับได้ · ค่านอกช่วงไม่ใช่ error แค่ปัดเข้าขอบ
func ClampWait(seconds int) int {
	if seconds < 0 {
		return 0
	}
	if seconds > maxWaitSeconds {
		return maxWaitSeconds
	}
	return seconds
}
