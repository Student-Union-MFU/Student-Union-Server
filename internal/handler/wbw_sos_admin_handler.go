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
)

type WBWSOSAdminHandler struct {
	service *service.WBWSOSAdminService
}

func NewWBWSOSAdminHandler(s *service.WBWSOSAdminService) *WBWSOSAdminHandler {
	return &WBWSOSAdminHandler{service: s}
}

// List GET /wbw/admin/sos?limit=200 — ทุกเคสของทั้งงาน รวมที่ปิดไปแล้ว
func (h *WBWSOSAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit")) // อ่านไม่ออก = 0 → repo ใช้ค่าตั้งต้น
	list, err := h.service.List(r.Context(), limit)
	if err != nil {
		slog.Error("โหลดรายการเคส SOS ของแอดมินไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายการไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Create POST /wbw/admin/sos — เปิดเคสแทนผู้เข้าร่วมที่แจ้งมาทางอื่น
func (h *WBWSOSAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.SOSAdminCreate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}
	actorID, actorName := actor(r)

	c, err := h.service.Create(r.Context(), req, actorID, actorName)
	switch {
	case err == nil:
		middleware.WriteJSON(w, http.StatusCreated, c)
	case errors.Is(err, service.ErrSOSNoParticipant):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องเลือกผู้เข้าร่วมก่อน")
	case errors.Is(err, service.ErrSOSBadSeverity):
		middleware.WriteError(w, http.StatusBadRequest, "ระดับความรุนแรงไม่ถูกต้อง")
	default:
		slog.Error("เปิดเคส SOS จากแผงผู้ดูแลไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "เปิดเคสไม่สำเร็จ")
	}
}

// Patch PATCH /wbw/admin/sos/{id} — แก้สถานะเคสด้วยมือ
func (h *WBWSOSAdminHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id, err := sosIDParam(r)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขเคสไม่ถูกต้อง")
		return
	}
	var p model.SOSAdminPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}
	actorID, actorName := actor(r)

	c, err := h.service.Patch(r.Context(), id, p, actorID, actorName)
	switch {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, c)
	case errors.Is(err, service.ErrSOSBadSeverity):
		middleware.WriteError(w, http.StatusBadRequest, "ระดับความรุนแรงไม่ถูกต้อง")
	case errors.Is(err, repository.ErrSOSNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบเคสนี้")
	case errors.Is(err, repository.ErrSOSAlreadyOpen):
		// เปิดเคสเก่าขึ้นมาใหม่ไม่ได้เพราะคนคนนี้มีเคสอื่นเปิดค้างอยู่ — กติกา
		// "หนึ่งคนหนึ่งเคสที่เปิดอยู่" เป็นของฐานข้อมูล ไม่ใช่ของหน้าจอ
		middleware.WriteError(w, http.StatusConflict, "ผู้เข้าร่วมคนนี้มีเคสที่ยังเปิดอยู่แล้ว")
	default:
		slog.Error("แก้สถานะเคส SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "แก้ไขไม่สำเร็จ")
	}
}
