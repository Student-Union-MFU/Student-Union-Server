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
	ErrSOSMissingClientID = errors.New("missing client_id")
	ErrSOSBadReason       = errors.New("unknown resolve reason")
)

// accuracyTrustMeters — แย่กว่านี้ห้ามผูกเคสกับฐานใดฐานหนึ่ง
//
// ฐานห่างกัน 300-500 ม. พิกัดที่ ±500 ม. จึงชี้ผิดฐานได้ง่ายๆ และการชี้ผิด
// แปลว่าทีมที่ถูกเรียกอยู่ไกลกว่าทีมที่ควรถูกเรียก ปล่อยให้ทุกคนเห็นดีกว่า
const accuracyTrustMeters = 200.0

const sosNotifyTimeout = 15 * time.Second

var validResolveReasons = map[string]bool{
	"helped": true, "false_alarm": true, "unreachable": true, "canceled_by_user": true,
}

type sosRepo interface {
	Checkpoints(ctx context.Context) ([]repository.CheckpointGeo, error)
	LastCheckinCheckpoint(ctx context.Context, participantID string) (*int, error)
	Raise(ctx context.Context, participantID string, req model.SOSRequest, checkpointID *int, locSource *string) (*model.SOSCase, bool, error)
	Get(ctx context.Context, id int64) (*model.SOSCase, error)
	GetForViewer(ctx context.Context, viewerID string, id int64) (*model.SOSCase, error)
	ActiveFor(ctx context.Context, participantID string) (*model.SOSCase, error)
	Cancel(ctx context.Context, participantID string, id int64) error
	Ack(ctx context.Context, id int64, staffID string) error
	Resolve(ctx context.Context, id int64, staffID, reason string) error
	StaffFeed(ctx context.Context, staffID, role, since string) ([]model.SOSStaffCase, error)
	CanStaffSee(ctx context.Context, staffID, role string, id int64) (bool, error)
	PushAudience(ctx context.Context, id int64) (*repository.SOSAudience, error)
	PushAllowed(ctx context.Context, id int64) (bool, error)
	MarkPushed(ctx context.Context, id int64) error
}

// sosPush / sosNoti — เฉพาะเมธอดที่ service นี้ใช้ ไม่ใช่ทั้ง WBWPushService
type sosPush interface {
	SendToTokens(ctx context.Context, tokens []string, title, body string, data map[string]string)
}

// ลายเซ็นต้องตรงกับ *WBWNotificationService.Create ของจริงเป๊ะ ไม่ใช่ตามที่อยากให้เป็น —
// มันคืน (*model.Notification, error) ไม่ใช่ (int64, error) · service นี้ทิ้งค่าที่คืนมาอยู่แล้ว
// แต่ถ้าลายเซ็นไม่ตรง cmd/main.go จะคอมไพล์ไม่ผ่านตอนเอา service จริงมาเสียบ
type sosNoti interface {
	Create(ctx context.Context, req model.NotificationRequest, actorID string) (*model.Notification, error)
}

// ยืนยันตอนคอมไพล์ว่า service จริงยังเข้ากับ interface นี้ได้ — ของปลอมในเทสยืนยันแทนไม่ได้
var _ sosNoti = (*WBWNotificationService)(nil)

// เพิ่มโดย Task 7 หลังจาก WBWPushService มีเมธอด SendToTokens แล้ว (ดู wbw_push_service.go)
// — ก่อนหน้านี้ assertion ตัวนี้เขียนไม่ได้เพราะเมธอดยังไม่มี ปล่อยให้คอมไพล์ผ่านด้วยของปลอมในเทสไปก่อน
var _ sosPush = (*WBWPushService)(nil)

type WBWSOSService struct {
	repo           sosRepo
	events         *SOSEvents
	push           sosPush
	noti           sosNoti
	emergencyPhone string
}

func NewWBWSOSService(repo sosRepo, events *SOSEvents, push sosPush, noti sosNoti, emergencyPhone string) *WBWSOSService {
	return &WBWSOSService{repo: repo, events: events, push: push, noti: noti, emergencyPhone: emergencyPhone}
}

// resolveBase — ฐานที่เคสนี้ควรถึงมือ พร้อมที่มาของพิกัด
//
// สามทางเรียงตามความน่าเชื่อ: GPS ที่แม่นพอ → ฐานล่าสุดที่เช็คอิน → ไม่รู้เลย
// ทางที่สามไม่ใช่ความล้มเหลว มันคือเคสที่ทุกคนต้องเห็นแทนที่จะเป็นเคสของใครคนเดียว
func (s *WBWSOSService) resolveBase(ctx context.Context, participantID string, req model.SOSRequest) (*int, *string) {
	none := "none"
	if req.Lat != nil && req.Lng != nil {
		if req.AccuracyM != nil && *req.AccuracyM > accuracyTrustMeters {
			gps := "gps"
			return nil, &gps // มีพิกัดจริงแต่หยาบเกินกว่าจะชี้ฐาน
		}
		points, err := s.repo.Checkpoints(ctx)
		if err == nil && len(points) > 0 {
			cps := make([]CheckpointPoint, 0, len(points))
			for _, p := range points {
				cps = append(cps, CheckpointPoint{ID: p.ID, Name: p.Name, Lat: p.Lat, Lng: p.Lng})
			}
			if near, _ := NearestCheckpoint(*req.Lat, *req.Lng, cps); near != nil {
				gps := "gps"
				id := near.ID
				return &id, &gps
			}
		}
	}
	if last, err := s.repo.LastCheckinCheckpoint(ctx, participantID); err == nil && last != nil {
		src := "last_checkin"
		return last, &src
	}
	return nil, &none
}

func (s *WBWSOSService) Raise(ctx context.Context, participantID string, req model.SOSRequest) (*model.SOSCase, bool, error) {
	if strings.TrimSpace(req.ClientID) == "" {
		return nil, false, ErrSOSMissingClientID
	}
	if req.Message != nil {
		trimmed := strings.TrimSpace(*req.Message)
		if trimmed == "" {
			req.Message = nil
		} else {
			req.Message = &trimmed
		}
	}

	checkpointID, locSource := s.resolveBase(ctx, participantID, req)
	c, created, err := s.repo.Raise(ctx, participantID, req, checkpointID, locSource)
	if err != nil {
		return nil, false, err
	}
	c.EmergencyPhone = s.emergencyPhone

	s.announce(ctx, c, created)
	return c, created, nil
}

// announce — ปลุก long-poll แล้วยิง push · fire-and-forget แบบเดียวกับ notifyFeedback
//
// context.WithoutCancel เพราะ ctx ของ request ถูกยกเลิกทันทีที่ตอบ response เสร็จ
// goSafe เพราะ chi.Recoverer ครอบแค่ goroutine ของ request — panic ตรงนี้จะฆ่าโปรเซสทั้งเครื่อง
func (s *WBWSOSService) announce(ctx context.Context, c *model.SOSCase, created bool) {
	detached := context.WithoutCancel(ctx)
	goSafe("sos.announce", func() {
		cc, cancel := context.WithTimeout(detached, sosNotifyTimeout)
		defer cancel()

		if err := s.events.Notify(cc); err != nil {
			slog.Error("ปลุก long-poll ของ SOS ไม่สำเร็จ", "err", err)
		}
		if s.push == nil {
			return
		}

		// ย้ำ (created = false) ยิงซ้ำได้ก็ต่อเมื่อห่างจากครั้งก่อน 60 วิ
		if !created {
			ok, err := s.repo.PushAllowed(cc, c.ID)
			if err != nil || !ok {
				return
			}
		}

		aud, err := s.repo.PushAudience(cc, c.ID)
		if err != nil {
			slog.Error("หาปลายทาง push ของ SOS ไม่สำเร็จ", "err", err)
			return
		}

		base := "ไม่ทราบตำแหน่ง"
		if c.CheckpointName != nil {
			base = "ใกล้" + *c.CheckpointName
		}
		title := "ขอความช่วยเหลือฉุกเฉิน"
		body := base
		data := map[string]string{"type": "sos", "sos_id": strconv.FormatInt(c.ID, 10)}

		s.push.SendToTokens(cc, aud.StaffTokens, title, body, data)
		s.push.SendToTokens(cc, aud.CentralTokens, title, body, data)

		// กลุ่มเพื่อนได้แค่ตอนเปิดกับตอนปิดเท่านั้น — ย้ำและอัปเดตพิกัดไม่ยิงซ้ำเข้ากลุ่ม
		if created {
			s.notifyGroup(cc, c, aud.GroupTokens, base)
		}
		if err := s.repo.MarkPushed(cc, c.ID); err != nil {
			slog.Error("จดเวลา push ของ SOS ไม่สำเร็จ", "err", err)
		}
	})
}

func (s *WBWSOSService) notifyGroup(ctx context.Context, c *model.SOSCase, tokens []string, base string) {
	verb := "ขอความช่วยเหลือ"
	if c.ForOther {
		verb = "แจ้งว่ามีคนเจ็บ"
	}
	title := "เพื่อนในกลุ่ม" + verb
	body := base + " · แตะเพื่อดูตำแหน่ง"

	if s.noti != nil {
		typ, audience, level := "sos", "group", "urgent"
		ref := strconv.FormatInt(c.ID, 10)
		if _, err := s.noti.Create(ctx, model.NotificationRequest{
			Type: &typ, Title: title, Body: &body, Level: &level,
			Audience: &audience, RefID: &ref,
		}, ""); err != nil {
			slog.Error("สร้างแถวแจ้งเตือน SOS ของกลุ่มไม่สำเร็จ", "err", err)
		}
	}
	s.push.SendToTokens(ctx, tokens, title, body,
		map[string]string{"type": "sos", "sos_id": strconv.FormatInt(c.ID, 10)})
}

func (s *WBWSOSService) Resolve(ctx context.Context, staffID, role string, id int64, reason string) error {
	if !validResolveReasons[reason] {
		return ErrSOSBadReason
	}
	ok, err := s.repo.CanStaffSee(ctx, staffID, role, id)
	if err != nil {
		return err
	}
	if !ok {
		return repository.ErrSOSNotFound
	}
	if err := s.repo.Resolve(ctx, id, staffID, reason); err != nil {
		return err
	}
	s.announceClosed(ctx, id)
	return nil
}

func (s *WBWSOSService) Cancel(ctx context.Context, participantID string, id int64) error {
	if err := s.repo.Cancel(ctx, participantID, id); err != nil {
		return err
	}
	s.announceClosed(ctx, id)
	return nil
}

func (s *WBWSOSService) Ack(ctx context.Context, staffID, role string, id int64) (*model.SOSCase, error) {
	ok, err := s.repo.CanStaffSee(ctx, staffID, role, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, repository.ErrSOSNotFound
	}
	// Ack ไม่แย่งของคนแรก (WHERE acked_at IS NULL ใน SQL) และไม่ถือเป็น error
	// อ่านเคสกลับไปเพื่อให้คนที่สองเห็นชื่อคนแรกแทนที่จะเห็นข้อความว่าล้มเหลว
	if err := s.repo.Ack(ctx, id, staffID); err != nil {
		return nil, err
	}
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	c.EmergencyPhone = s.emergencyPhone
	detached := context.WithoutCancel(ctx)
	goSafe("sos.ack.notify", func() {
		cc, cancel := context.WithTimeout(detached, sosNotifyTimeout)
		defer cancel()
		if err := s.events.Notify(cc); err != nil {
			slog.Error("ปลุก long-poll หลังรับเรื่องไม่สำเร็จ", "err", err)
		}
	})
	return c, nil
}

func (s *WBWSOSService) Active(ctx context.Context, participantID string, wait int) (*model.SOSCase, error) {
	c, err := s.repo.ActiveFor(ctx, participantID)
	if err != nil {
		return nil, err
	}
	// ไม่มีอะไรเปลี่ยน และผู้เรียกยอมรอ — จอดไว้จนมีเหตุการณ์หรือหมดเวลา
	if wait > 0 {
		if s.events.Wait(ctx, time.Duration(wait)*time.Second) {
			c, err = s.repo.ActiveFor(ctx, participantID)
			if err != nil {
				return nil, err
			}
		}
	}
	if c != nil {
		c.EmergencyPhone = s.emergencyPhone
	}
	return c, nil
}

// Get — เคสหนึ่งอันสำหรับคนกดเองหรือเพื่อนในกลุ่มเดียวกัน
// คนนอกกลุ่มได้ ErrSOSNotFound ไม่ใช่ 403 — การบอกว่า "มีเคสนี้อยู่แต่คุณดูไม่ได้"
// ก็เป็นการรั่วข้อมูลอยู่ดี
func (s *WBWSOSService) Get(ctx context.Context, viewerID string, id int64) (*model.SOSCase, error) {
	c, err := s.repo.GetForViewer(ctx, viewerID, id)
	if err != nil {
		return nil, err
	}
	c.EmergencyPhone = s.emergencyPhone
	return c, nil
}

func (s *WBWSOSService) StaffFeed(ctx context.Context, staffID, role, since string, wait int) ([]model.SOSStaffCase, error) {
	list, err := s.repo.StaffFeed(ctx, staffID, role, since)
	if err != nil {
		return nil, err
	}
	// มีของอยู่แล้วก็ตอบเลย — long-poll มีไว้รอ "ของใหม่" ไม่ใช่หน่วงของที่มีอยู่
	if len(list) > 0 || wait <= 0 {
		return list, nil
	}
	if s.events.Wait(ctx, time.Duration(wait)*time.Second) {
		return s.repo.StaffFeed(ctx, staffID, role, since)
	}
	return list, nil
}

// announceClosed — บอกกลุ่มว่าเรื่องจบแล้ว
//
// ไม่บอกเหตุผลจริงกับทั้งกลุ่ม — false_alarm เป็นเรื่องระหว่างคนกดกับเจ้าหน้าที่
// แต่ต้องบอกว่าจบ ไม่งั้นคน 50 คนค้างอยู่กับความกังวลทั้งวัน
// นี่คือ push ตัวที่สองและตัวสุดท้ายที่กลุ่มจะได้รับสำหรับเคสนี้
func (s *WBWSOSService) announceClosed(ctx context.Context, id int64) {
	detached := context.WithoutCancel(ctx)
	goSafe("sos.announceClosed", func() {
		cc, cancel := context.WithTimeout(detached, sosNotifyTimeout)
		defer cancel()

		if err := s.events.Notify(cc); err != nil {
			slog.Error("ปลุก long-poll หลังปิดเคสไม่สำเร็จ", "err", err)
		}
		if s.push == nil {
			return
		}
		c, err := s.repo.Get(cc, id)
		if err != nil {
			slog.Error("อ่านเคสที่เพิ่งปิดไม่สำเร็จ", "err", err)
			return
		}
		aud, err := s.repo.PushAudience(cc, id)
		if err != nil {
			return
		}
		title := "เรื่องขอความช่วยเหลือจบแล้ว"
		body := "เจ้าหน้าที่ดูแลเรียบร้อยแล้ว"
		if c.ResolveReason != nil && *c.ResolveReason == "canceled_by_user" {
			body = "ยกเลิกแล้ว"
		}
		s.push.SendToTokens(cc, aud.GroupTokens, title, body,
			map[string]string{"type": "sos", "sos_id": strconv.FormatInt(id, 10)})
	})
}
