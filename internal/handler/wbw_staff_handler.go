package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"
)

type WBWStaffHandler struct {
	service *service.WBWStaffService
}

func NewWBWStaffHandler(s *service.WBWStaffService) *WBWStaffHandler {
	return &WBWStaffHandler{service: s}
}

// Checkpoints GET /wbw/staff/checkpoints — ฐานที่ตัวเองประจำ (admin เห็นทุกฐาน)
func (h *WBWStaffHandler) Checkpoints(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	list, err := h.service.Checkpoints(r.Context(), claims.Subject, claims.Role)
	if err != nil {
		slog.Error("list staff checkpoints failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดฐานไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Checkin POST /wbw/staff/checkin — เช็คอินผู้เข้าร่วมที่ฐาน จาก QR หรือ BIB
func (h *WBWStaffHandler) Checkin(w http.ResponseWriter, r *http.Request) {
	var req model.StaffCheckinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	uid, _ := actor(r)

	out, err := h.service.Checkin(r.Context(), uid, req)
	switch {
	case errors.Is(err, service.ErrMissingCheckpoint):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องระบุฐาน")
	case errors.Is(err, service.ErrMissingIdentifier):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี QR หรือหมายเลข BIB")
	case errors.Is(err, repository.ErrParticipantNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบผู้เข้าร่วม")
	case err != nil:
		slog.Error("staff checkin failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "เช็คอินไม่สำเร็จ")
	default:
		// already_checked_in ไม่ใช่ error — staff ต้องเห็นชื่อคนอยู่ดี
		middleware.WriteJSON(w, http.StatusOK, out)
	}
}

/* ---------- device token (push) ---------- */

type WBWDeviceHandler struct {
	service *service.WBWDeviceService
}

func NewWBWDeviceHandler(s *service.WBWDeviceService) *WBWDeviceHandler {
	return &WBWDeviceHandler{service: s}
}

// Register POST /wbw/devices/register
func (h *WBWDeviceHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req model.DeviceRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี token และ platform")
		return
	}
	uid, _ := actor(r)

	err := h.service.Register(r.Context(), uid, req)
	switch {
	case errors.Is(err, service.ErrMissingToken):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี token และ platform")
	case err != nil:
		slog.Error("register device failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลงทะเบียนอุปกรณ์ไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusCreated, map[string]bool{"ok": true})
	}
}

// Unregister POST /wbw/devices/unregister
func (h *WBWDeviceHandler) Unregister(w http.ResponseWriter, r *http.Request) {
	var req model.DeviceUnregisterRequest
	// body ว่างก็ถือว่าสำเร็จ — logout ตอนยังไม่เคยลงทะเบียน push ไม่ควรพัง
	_ = json.NewDecoder(r.Body).Decode(&req)
	uid, _ := actor(r)

	if err := h.service.Unregister(r.Context(), uid, req); err != nil {
		slog.Error("unregister device failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ถอนอุปกรณ์ไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
