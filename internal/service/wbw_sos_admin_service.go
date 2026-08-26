package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"
)

/* ============================================================
   งานฝั่งแอดมินของ SOS — แยก service ออกมาโดยตั้งใจ

   WBWSOSService รับ repo ผ่าน interface sosRepo ซึ่งของปลอมในเทสหลายตัว implement
   ไว้ครบแล้ว · การเพิ่มเมธอดแอดมินเข้าไปใน interface นั้นแปลว่าของปลอมทุกตัวใน
   ชุดเทสต้อง implement เมธอดที่มันไม่ได้ทดสอบด้วย ซึ่งไม่ได้ทำให้อะไรปลอดภัยขึ้น
   ตัวนี้ถือ repository ตัวจริงตรง ๆ เพราะมันใช้ query ที่มีอยู่ที่เดียวจริง ๆ

   แต่ยังยืม WBWSOSService มาเปิดเคส แทนที่จะ insert เอง — เคสที่แอดมินเปิดต้อง
   ปลุก long-poll และยิง push เหมือนเคสที่กดจากแอปเป๊ะ ๆ ถ้าเขียน insert เส้นใหม่
   เคสจะโผล่ในฐานข้อมูลแต่ไม่มีเจ้าหน้าที่คนไหนรู้ว่ามันเกิดขึ้น
   ============================================================ */

var (
	// ErrSOSBadSeverity — ระดับนอกสามค่าที่ CHECK constraint ยอมรับ
	ErrSOSBadSeverity = errors.New("bad severity")
	// ErrSOSNoParticipant — เปิดเคสโดยไม่บอกว่าเป็นของใคร
	ErrSOSNoParticipant = errors.New("participant required")
)

var validSeverity = map[string]bool{"minor": true, "major": true, "urgent": true}

type WBWSOSAdminService struct {
	repo  *repository.WBWSOSRepository
	sos   *WBWSOSService
	admin *repository.WBWAdminRepository
}

func NewWBWSOSAdminService(
	repo *repository.WBWSOSRepository, sos *WBWSOSService, admin *repository.WBWAdminRepository,
) *WBWSOSAdminService {
	return &WBWSOSAdminService{repo: repo, sos: sos, admin: admin}
}

func (s *WBWSOSAdminService) List(ctx context.Context, limit int) ([]model.SOSStaffCase, error) {
	return s.repo.AdminList(ctx, limit)
}

// newClientID — UUID v4 สำหรับ client_id ของเคสที่เปิดจากแผงเว็บ
//
// client_id มีไว้ให้แอปมือถือส่งซ้ำได้โดยไม่กลายเป็นสองเคส (outbox retry ตอนเน็ต
// หลุดบนดอย) · แผงเว็บไม่มี outbox — กดแล้วเห็นผลทันที กดซ้ำคือเจตนาเปิดอีกเคส
// จึงสร้างค่าใหม่ทุกครั้งฝั่งเซิร์ฟเวอร์ แทนที่จะให้ browser คิดเอง
//
// ไม่ดึง lib uuid เข้ามาเพื่อใช้จุดเดียว — v4 คือสุ่ม 16 ไบต์แล้วตั้ง version/variant
func newClientID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]), nil
}

// Create — แอดมินเปิดเคสแทนผู้เข้าร่วม (แจ้งทางวิทยุ/โทรศัพท์/เดินมาบอก)
func (s *WBWSOSAdminService) Create(
	ctx context.Context, req model.SOSAdminCreate, actorID, actorName string,
) (*model.SOSStaffCase, error) {
	if strings.TrimSpace(req.ParticipantID) == "" {
		return nil, ErrSOSNoParticipant
	}
	if req.Severity != nil && *req.Severity != "" && !validSeverity[*req.Severity] {
		return nil, ErrSOSBadSeverity
	}

	clientID, err := newClientID()
	if err != nil {
		return nil, err
	}
	raise := model.SOSRequest{
		ClientID: clientID,
		// device_time = เวลาที่แอดมินกด · ไม่ใช่เวลาที่เหตุเกิดจริง ซึ่งไม่มีใครรู้
		// จากแผงนี้ · เวลาที่นับสถิติตอบสนองคือ server_received_at อยู่แล้ว
		DeviceTime: time.Now().UTC().Format(time.RFC3339),
		ForOther:   req.ForOther,
		Lat:        req.Lat,
		Lng:        req.Lng,
		Message:    req.Message,
	}

	c, _, err := s.sos.Raise(ctx, req.ParticipantID, raise)
	if err != nil {
		return nil, err
	}

	// ระดับความรุนแรงตั้งแยกหลังเปิดเคส — Raise ไม่รับ severity เพราะเคสจากแอปไม่มี
	// ใครประเมินตอนกด · แอดมินที่รับแจ้งทางวิทยุมักรู้แล้วว่าหนักแค่ไหน จึงให้ตั้งได้เลย
	if req.Severity != nil && *req.Severity != "" {
		if _, err := s.repo.AdminPatch(ctx, c.ID, model.SOSAdminPatch{Severity: req.Severity}, actorID); err != nil {
			return nil, err
		}
	}

	s.log(ctx, actorID, actorName, "sos_create", fmt.Sprintf("เปิดเคส #%d แทนผู้เข้าร่วม", c.ID))
	return s.repo.AdminGet(ctx, c.ID)
}

// Patch — แอดมินแก้สถานะเคส (ระดับ / ยกระดับ / ปิด / เปิดใหม่)
func (s *WBWSOSAdminService) Patch(
	ctx context.Context, id int64, p model.SOSAdminPatch, actorID, actorName string,
) (*model.SOSStaffCase, error) {
	if p.Severity != nil && *p.Severity != "" && !validSeverity[*p.Severity] {
		return nil, ErrSOSBadSeverity
	}

	before, err := s.repo.AdminGet(ctx, id)
	if err != nil {
		return nil, err
	}

	c, err := s.repo.AdminPatch(ctx, id, p, actorID)
	if err != nil {
		return nil, err
	}

	// ปิดเคสจากแผงต้องแจ้งออกไปเหมือนเจ้าหน้าที่กดปิดหน้างาน — คนที่รออยู่ในแอป
	// (ตัวคนกดและเพื่อนในกลุ่ม) ต้องเห็นว่าเรื่องจบแล้ว ไม่ใช่ค้างหน้าจอรอต่อไป
	// เช็ค before ด้วย เพื่อไม่ยิงซ้ำตอนแอดมินแก้อย่างอื่นของเคสที่ปิดไปแล้ว
	if p.Resolved != nil && *p.Resolved && !before.Resolved {
		s.sos.announceClosed(ctx, id)
	}

	s.log(ctx, actorID, actorName, "sos_override", describePatch(id, p))
	return c, nil
}

// describePatch — บรรทัดเดียวที่อ่านรู้เรื่องใน activity log
//
// เก็บว่า "เปลี่ยนอะไร" ไม่ใช่แค่ "แก้เคส #12" — เคสฉุกเฉินที่ถูกปิดด้วยมือคือ
// สิ่งที่คนจะย้อนกลับมาถามทีหลังว่าใครปิดและปิดทำไม
func describePatch(id int64, p model.SOSAdminPatch) string {
	parts := []string{}
	if p.Severity != nil {
		if *p.Severity == "" {
			parts = append(parts, "ล้างระดับความรุนแรง")
		} else {
			parts = append(parts, "ระดับ="+*p.Severity)
		}
	}
	if p.Escalated != nil {
		if *p.Escalated {
			parts = append(parts, "ยกระดับเป็น SOS จริง")
		} else {
			parts = append(parts, "ถอนการยกระดับ")
		}
	}
	if p.Resolved != nil {
		if *p.Resolved {
			r := "ปิดเคส"
			if p.Reason != nil && strings.TrimSpace(*p.Reason) != "" {
				r += " (" + strings.TrimSpace(*p.Reason) + ")"
			}
			parts = append(parts, r)
		} else {
			parts = append(parts, "เปิดเคสขึ้นมาใหม่")
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "ไม่มีการเปลี่ยนแปลง")
	}
	return fmt.Sprintf("เคส #%d — %s", id, strings.Join(parts, " · "))
}

// log — เขียน admin_log · ไม่คืน error เพราะการบันทึกล้มเหลวไม่ควรทำให้การกระทำ
// ที่สำเร็จไปแล้วดูเหมือนล้มเหลวกับคนกด (LogAction ฝั่ง repo กลืน error เองอยู่แล้ว)
func (s *WBWSOSAdminService) log(ctx context.Context, actorID, actorName, action, detail string) {
	if s.admin == nil {
		return
	}
	s.admin.LogAction(ctx, actorID, actorName, action, detail)
}
