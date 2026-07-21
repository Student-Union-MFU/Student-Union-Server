package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/service"
)

type WBWAuthHandler struct {
	service *service.WBWAuthService
}

func NewWBWAuthHandler(s *service.WBWAuthService) *WBWAuthHandler {
	return &WBWAuthHandler{service: s}
}

func (h *WBWAuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	res, err := h.service.Register(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrBadStudentID):
			middleware.WriteError(w, http.StatusBadRequest, "รหัสนักศึกษาต้อง 10 หลัก ขึ้นต้น 693 (นักศึกษาชั้นปีที่ 1)")
		case errors.Is(err, service.ErrShortPassword):
			middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านต้องยาวอย่างน้อย 8 ตัว")
		case errors.Is(err, service.ErrHasChronic):
			middleware.WriteError(w, http.StatusBadRequest, "กิจกรรมนี้เปิดรับเฉพาะผู้ไม่มีโรคประจำตัว")
		case errors.Is(err, service.ErrDuplicateUser):
			middleware.WriteError(w, http.StatusConflict, "รหัสนักศึกษานี้สมัครไปแล้ว")
		default:
			slog.Error("wbw register failed", "err", err)
			middleware.WriteError(w, http.StatusInternalServerError, "สมัครไม่สำเร็จ")
		}
		return
	}
	middleware.WriteJSON(w, http.StatusCreated, res)
}

func (h *WBWAuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี username และ password")
		return
	}

	res, err := h.service.Login(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrMissingFields):
			middleware.WriteError(w, http.StatusBadRequest, "ต้องมี username และ password")
		case errors.Is(err, service.ErrBadCredentials):
			middleware.WriteError(w, http.StatusUnauthorized, "username หรือ password ไม่ถูกต้อง")
		default:
			slog.Error("wbw login failed", "err", err)
			middleware.WriteError(w, http.StatusInternalServerError, "ล็อกอินไม่สำเร็จ")
		}
		return
	}
	middleware.WriteJSON(w, http.StatusOK, res)
}
