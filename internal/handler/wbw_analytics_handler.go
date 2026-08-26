package handler

import (
	"log/slog"
	"net/http"

	"su-server/internal/middleware"
	"su-server/internal/service"
)

type WBWAnalyticsHandler struct {
	service *service.WBWAnalyticsService
}

func NewWBWAnalyticsHandler(s *service.WBWAnalyticsService) *WBWAnalyticsHandler {
	return &WBWAnalyticsHandler{service: s}
}

// Analytics GET /wbw/admin/analytics — ก้อนสรุปทั้งหมดสำหรับแท็บ "วิเคราะห์"
//
// ไม่มี query param ให้กรอง: ทุกกราฟบนหน้าเดียวกันต้องมาจาก snapshot เดียวกัน
// ถ้าเปิดให้กรองทีละกราฟ หน้าจะกลายเป็นภาพผสมของหลายช่วงเวลาที่เทียบกันไม่ได้
// การกรอง/ซูมทำฝั่งเว็บบนก้อนเดียวกันนี้แทน
func (h *WBWAnalyticsHandler) Analytics(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.Analytics(r.Context())
	if err != nil {
		slog.Error("analytics failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดสถิติไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}
