package handler

import (
	_ "embed"
	"log/slog"
	"net/http"

	appmw "su-server/internal/middleware"
	"su-server/internal/service"
)

// The console is one file, embedded so the Dockerfile's single-binary build
// stays true — no static directory to COPY and no path to get wrong at runtime.
//
//go:embed clubfair_dashboard.html
var clubFairDashboardHTML []byte

type ClubFairAdminHandler struct {
	service *service.ClubFairAdminService
}

func NewClubFairAdminHandler(s *service.ClubFairAdminService) *ClubFairAdminHandler {
	return &ClubFairAdminHandler{service: s}
}

// GET /clubfair/dashboard — the admin console page.
//
// Served without auth, deliberately: a browser navigation cannot carry a Bearer
// header, so the gate is on the data, not the page. The HTML holds no numbers —
// its script signs in, checks the role, and only then calls the endpoint below.
func (h *ClubFairAdminHandler) DashboardPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(clubFairDashboardHTML)
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
