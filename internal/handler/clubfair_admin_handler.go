package handler

import (
	"log/slog"
	"net/http"

	appmw "su-server/internal/middleware"
	"su-server/internal/service"
)

type ClubFairAdminHandler struct {
	service *service.ClubFairAdminService
}

func NewClubFairAdminHandler(s *service.ClubFairAdminService) *ClubFairAdminHandler {
	return &ClubFairAdminHandler{service: s}
}

// GET /clubfair/admin/dashboard — the fair at a glance. Admin only.
func (h *ClubFairAdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.Dashboard(r.Context())
	if err != nil {
		slog.Error("clubfair dashboard failed", "err", err)
		appmw.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโหลดข้อมูลสรุปได้")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, stats)
}
