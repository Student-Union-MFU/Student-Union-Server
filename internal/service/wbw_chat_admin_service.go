package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"su-server/internal/model"
	"su-server/internal/repository"
)

/* ============================================================
   การจัดการแชทโดยผู้ดูแล

   แยกจาก WBWChatService ด้วยเหตุผลเดียวกับ SOS: service เดิมรับ repo ผ่านสิ่งที่
   ของปลอมในเทสอิงอยู่ และเมธอดพวกนี้ไม่มีอะไรร่วมกับเส้นทางส่งข้อความปกติเลย
   นอกจากตารางเดียวกัน

   ทุกการกระทำถูกบันทึกลง admin_log พร้อมเลขข้อความและกลุ่ม — การลบข้อความคนอื่น
   คือสิ่งที่ต้องตอบได้ทีหลังว่าใครทำและทำไม
   ============================================================ */

var (
	// ErrBadChatAction — action ที่ไม่รู้จัก · ปฏิเสธไปตรง ๆ ดีกว่าเดาว่าหมายถึงอะไร
	ErrBadChatAction = errors.New("unknown moderation action")
)

// defaultCensorMask — ข้อความที่ผู้เข้าร่วมเห็นแทนเมื่อผู้ดูแลไม่ได้พิมพ์ของตัวเอง
//
// บอกว่า "ถูกซ่อน" ไม่ใช่ทำเป็นว่าไม่เคยมีข้อความ — คนในห้องเห็นอยู่แล้วว่ามีคน
// พิมพ์อะไรสักอย่าง (แอปแสดงทันทีที่ส่ง) การทำให้หายไปเงียบ ๆ ทำให้คนคิดว่าแอปพัง
const defaultCensorMask = "ข้อความนี้ถูกซ่อนโดยผู้ดูแล"

type WBWChatAdminService struct {
	chat  *repository.WBWChatRepository
	admin *repository.WBWAdminRepository
	// events — ปลุก long-poll หลังแก้ข้อความ เพื่อให้เครื่องที่เปิดห้องค้างอยู่
	// ดึงรอบใหม่ · ดูข้อจำกัดที่ Moderate
	events *ChatEvents
}

func NewWBWChatAdminService(
	chat *repository.WBWChatRepository, admin *repository.WBWAdminRepository, events *ChatEvents,
) *WBWChatAdminService {
	return &WBWChatAdminService{chat: chat, admin: admin, events: events}
}

func (s *WBWChatAdminService) Rooms(ctx context.Context) ([]model.ChatRoomSummary, error) {
	return s.chat.Rooms(ctx)
}

func (s *WBWChatAdminService) RoomMessages(ctx context.Context, groupID, limit int) ([]model.AdminMessage, error) {
	return s.chat.RoomMessages(ctx, groupID, limit)
}

func (s *WBWChatAdminService) Search(ctx context.Context, q string, limit int) ([]model.AdminMessage, error) {
	if strings.TrimSpace(q) == "" {
		return []model.AdminMessage{}, nil
	}
	return s.chat.SearchMessages(ctx, q, limit)
}

// Moderate — ลบ / กู้คืน / เซ็นเซอร์ / ยกเลิกเซ็นเซอร์ ข้อความหนึ่ง
//
// ⚠ ข้อจำกัดที่ต้องรู้: แอปมือถือ sync แบบ "ขอข้อความหลัง id นี้" เท่านั้น เครื่อง
// ที่รับข้อความไปแล้วจะยังแสดงของเดิมจนกว่าจะโหลดห้องใหม่ · การปลุก long-poll
// ตรงนี้ทำให้เครื่องที่เปิดค้างอยู่ตื่นขึ้นมาถามต่อ แต่มันจะได้เฉพาะข้อความที่
// "ใหม่กว่า" cursor ของตัวเอง ไม่ใช่ของเก่าที่เพิ่งถูกแก้
//
// พูดให้ตรง: การลบมีผลกับคนที่ยังไม่ได้อ่าน และคนที่เปิดห้องใหม่ ไม่ใช่การถอน
// ข้อความคืนจากเครื่องที่เห็นไปแล้ว · การแก้ให้ครบต้องมีช่องทางส่ง "เหตุการณ์
// การแก้ไข" แยกจากสตรีมข้อความ ซึ่งต้องแก้ทั้งฝั่งแอป — ไม่ได้ทำในรอบนี้
func (s *WBWChatAdminService) Moderate(
	ctx context.Context, id int64, req model.ChatModerateRequest, actorID, actorName string,
) (*model.AdminMessage, error) {
	var (
		m    *model.AdminMessage
		err  error
		what string
	)

	switch req.Action {
	case "delete":
		m, err = s.chat.DeleteMessage(ctx, id, actorID)
		what = "ลบข้อความ"
	case "restore":
		m, err = s.chat.RestoreMessage(ctx, id)
		what = "กู้คืนข้อความที่ลบไว้"
	case "censor":
		mask := strings.TrimSpace(req.Replacement)
		if mask == "" {
			mask = defaultCensorMask
		}
		m, err = s.chat.CensorMessage(ctx, id, mask, actorID)
		what = "เซ็นเซอร์ข้อความ"
	case "uncensor":
		m, err = s.chat.UncensorMessage(ctx, id)
		what = "ยกเลิกการเซ็นเซอร์"
	default:
		return nil, ErrBadChatAction
	}
	if err != nil {
		return nil, err
	}

	if s.events != nil {
		// ปลุกเฉพาะห้องที่ถูกแก้ · actor เป็น "" เพื่อไม่ให้ใครถูกตัดออกจากการปลุก
		// (NotifyGroup ตัดผู้ส่งออกจากการปลุกตัวเอง ซึ่งที่นี่ไม่มีผู้ส่ง)
		// NotifyGroup กลืน error เองอยู่แล้ว — การปลุกไม่สำเร็จแปลว่าเห็นช้าลง
		// ไม่ใช่ว่าการลบไม่สำเร็จ ซึ่ง commit ไปแล้ว
		s.events.NotifyGroup(ctx, m.GroupID, "")
	}
	if s.admin != nil {
		s.admin.LogAction(ctx, actorID, actorName, "chat_moderate",
			fmt.Sprintf("%s #%d (กลุ่ม %d)", what, id, m.GroupID))
	}
	return m, nil
}
