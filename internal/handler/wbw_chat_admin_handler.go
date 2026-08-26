package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

type WBWChatAdminHandler struct {
	service *service.WBWChatAdminService
}

func NewWBWChatAdminHandler(s *service.WBWChatAdminService) *WBWChatAdminHandler {
	return &WBWChatAdminHandler{service: s}
}

// Rooms GET /wbw/admin/chat — ทุกกลุ่ม พร้อมยอดข้อความและยอดที่ถูกจัดการไปแล้ว
func (h *WBWChatAdminHandler) Rooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := h.service.Rooms(r.Context())
	if err != nil {
		slog.Error("โหลดรายการห้องแชทไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายการห้องไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, rooms)
}

// Messages GET /wbw/admin/chat/{groupId}?limit=500 — ข้อความทั้งห้อง
//
// ไม่มี long-poll ตรงนี้ต่างจากฝั่งผู้ใช้: หน้าผู้ดูแลเป็นหน้าตรวจสอบ ไม่ใช่ห้อง
// สนทนา · การถือ connection ค้างไว้ต่อห้องต่อแอดมินคือการจ่ายค่าที่ไม่ได้ใช้
func (h *WBWChatAdminHandler) Messages(w http.ResponseWriter, r *http.Request) {
	groupID, err := strconv.Atoi(chi.URLParam(r, "groupId"))
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขกลุ่มไม่ถูกต้อง")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	list, err := h.service.RoomMessages(r.Context(), groupID, limit)
	if err != nil {
		slog.Error("โหลดข้อความของห้องไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อความไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Search GET /wbw/admin/chat/search?q=...&limit=200 — ค้นข้ามทุกห้อง
func (h *WBWChatAdminHandler) Search(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.service.Search(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		slog.Error("ค้นข้อความไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ค้นหาไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Moderate POST /wbw/admin/chat/messages/{id} — ลบ / กู้คืน / เซ็นเซอร์
//
// POST ไม่ใช่ PATCH หรือ DELETE เพราะสิ่งที่ส่งไม่ใช่ "สถานะใหม่ของทรัพยากร" แต่
// เป็นคำสั่งที่ระบบตีความ (censor เขียนสองคอลัมน์และย้ายค่าเดิม) · DELETE ก็ผิด
// เพราะทั้งสี่ action ใช้ทางเดียวกันและสองในสี่คือการ "คืนค่า"
func (h *WBWChatAdminHandler) Moderate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขข้อความไม่ถูกต้อง")
		return
	}
	var req model.ChatModerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}
	actorID, actorName := actor(r)

	m, err := h.service.Moderate(r.Context(), id, req, actorID, actorName)
	switch {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, m)
	case errors.Is(err, service.ErrBadChatAction):
		middleware.WriteError(w, http.StatusBadRequest, "คำสั่งไม่ถูกต้อง")
	case errors.Is(err, repository.ErrMessageNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบข้อความนี้")
	default:
		slog.Error("จัดการข้อความไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ดำเนินการไม่สำเร็จ")
	}
}
