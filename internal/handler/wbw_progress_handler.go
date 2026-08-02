package handler

import (
	"log/slog"
	"net/http"

	"su-server/internal/middleware"
	"su-server/internal/service"
)

type WBWProgressHandler struct {
	service *service.WBWProgressService
}

func NewWBWProgressHandler(s *service.WBWProgressService) *WBWProgressHandler {
	return &WBWProgressHandler{service: s}
}

// MyProgress GET /wbw/me/progress — ฐานที่ตัวเองเช็คอินแล้ว + จำนวนฐานทั้งหมด
func (h *WBWProgressHandler) MyProgress(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	p, err := h.service.MyProgress(r.Context(), claims.Subject)
	if err != nil {
		slog.Error("load checkin progress failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดความคืบหน้าไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, p)
}
