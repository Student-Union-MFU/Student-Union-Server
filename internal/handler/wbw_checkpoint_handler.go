package handler

import (
	"log/slog"
	"net/http"

	"su-server/internal/middleware"
	"su-server/internal/service"
)

type WBWCheckpointHandler struct {
	service *service.WBWCheckpointService
}

func NewWBWCheckpointHandler(s *service.WBWCheckpointService) *WBWCheckpointHandler {
	return &WBWCheckpointHandler{service: s}
}

// List GET /wbw/checkpoints — ฐานทั้งงาน (ผู้เข้าร่วมที่ล็อกอินแล้วอ่านได้)
//
// แอปเอาไปขึ้นชื่อฐานบนการ์ดในแท็บแผนที่ · ก่อนมี endpoint นี้ แอปรู้ชื่อเฉพาะฐานที่เช็คอินไปแล้ว
// (จาก /me/progress ที่คืนแค่ checked_in) ฐานที่ยังไม่ไปถึงจึงขึ้นว่า "ฐานที่ N" และแอดมินแก้ชื่อ
// บนแดชบอร์ดแล้วแอปก็ไม่รู้เรื่อง
func (h *WBWCheckpointHandler) List(w http.ResponseWriter, r *http.Request) {
	if middleware.ClaimsFrom(r.Context()) == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	list, err := h.service.List(r.Context())
	if err != nil {
		slog.Error("list participant checkpoints failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลฐานไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}
