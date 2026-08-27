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
		case errors.Is(err, service.ErrEventFull):
			// 409 เหมือนกรณีสมัครซ้ำ — เป็นเรื่องสถานะของข้อมูล ไม่ใช่เซิร์ฟเวอร์มีปัญหา
			// (503 จะทำให้ client/CDN เข้าใจว่าลองใหม่แล้วจะได้ ซึ่งไม่จริง)
			middleware.WriteError(w, http.StatusConflict, "ที่นั่งเต็มแล้ว — ปิดรับสมัคร")
		default:
			slog.Error("wbw register failed", "err", err)
			middleware.WriteError(w, http.StatusInternalServerError, "สมัครไม่สำเร็จ")
		}
		return
	}
	middleware.WriteJSON(w, http.StatusCreated, res)
}

func (h *WBWAuthHandler) RegisterStaff(w http.ResponseWriter, r *http.Request) {
	var req model.StaffRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	user, err := h.service.RegisterStaff(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrMissingFields):
			middleware.WriteError(w, http.StatusBadRequest, "กรุณากรอกชื่อผู้ใช้และเลือกสำนักวิชา")
		case errors.Is(err, service.ErrShortPassword):
			middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านต้องยาวอย่างน้อย 8 ตัว")
		case errors.Is(err, service.ErrBadStaffRole):
			middleware.WriteError(w, http.StatusBadRequest, "กรุณาเลือกหน้าที่ในงาน")
		case errors.Is(err, service.ErrBadEmail):
			middleware.WriteError(w, http.StatusBadRequest, "กรุณากรอกอีเมลมหาวิทยาลัย (@mfu.ac.th หรือ @lamduan.mfu.ac.th)")
		case errors.Is(err, service.ErrEmailTaken):
			middleware.WriteError(w, http.StatusConflict, "อีเมลนี้ถูกใช้กับบัญชีอื่นแล้ว")
		case errors.Is(err, service.ErrDuplicateUser):
			middleware.WriteError(w, http.StatusConflict, "ชื่อผู้ใช้นี้มีอยู่แล้ว")
		case repository.IsPGCode(err, "23503"):
			middleware.WriteError(w, http.StatusBadRequest, "ไม่พบสำนักวิชาที่เลือก")
		default:
			slog.Error("wbw staff register failed", "err", err)
			middleware.WriteError(w, http.StatusInternalServerError, "สมัครไม่สำเร็จ")
		}
		return
	}
	// pending=true บอก frontend ให้ขึ้นหน้า "รออนุมัติ" (ไม่มี token ให้ล็อกอิน)
	middleware.WriteJSON(w, http.StatusCreated, map[string]any{"user": user, "pending": true})
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
		case errors.Is(err, service.ErrPendingApproval):
			middleware.WriteError(w, http.StatusForbidden, "บัญชีเจ้าหน้าที่กำลังรอผู้ดูแลอนุมัติ")
		default:
			slog.Error("wbw login failed", "err", err)
			middleware.WriteError(w, http.StatusInternalServerError, "ล็อกอินไม่สำเร็จ")
		}
		return
	}
	middleware.WriteJSON(w, http.StatusOK, res)
}

// Forgot — POST /wbw/auth/forgot · เปิดสาธารณะ ส่งลิงก์ตั้งรหัสผ่านใหม่ไปที่อีเมล
// ของเจ้าของบัญชี
//
// ⚠ ตอบ 200 เหมือนกันหมด ไม่ว่าจะมีบัญชีนั้นจริงหรือไม่ · ข้อความที่หน้าเว็บขึ้นต้อง
// เป็น "ถ้ามีบัญชีนี้อยู่ เราส่งลิงก์ไปให้แล้ว" ไม่ใช่ "ส่งแล้ว" — endpoint ที่แยก
// สองกรณีได้คือเครื่องมือไล่เช็คว่ารหัสนักศึกษาไหนสมัครงานนี้ไว้ (ดูเหตุผลเต็มที่
// WBWAuthService.RequestPasswordReset) error เดียวที่โผล่ที่นี่ได้คือฐานข้อมูลพัง
func (h *WBWAuthHandler) Forgot(w http.ResponseWriter, r *http.Request) {
	var req model.ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	if err := h.service.RequestPasswordReset(r.Context(), req.Username); err != nil {
		slog.Error("wbw forgot password failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ขอลิงก์ตั้งรหัสผ่านใหม่ไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Reset — POST /wbw/auth/reset · แลกตั๋วจากลิงก์ในอีเมลเป็นรหัสผ่านใหม่
//
// ไม่คืน token ให้ล็อกอินต่อทันทีโดยตั้งใจ: คนที่เพิ่งตั้งรหัสใหม่ควรได้พิสูจน์ว่า
// จำรหัสที่เพิ่งตั้งได้จริงก่อน และหน้าเว็บมีที่ให้เด้งกลับอยู่แล้ว (หน้าล็อกอิน)
func (h *WBWAuthHandler) Reset(w http.ResponseWriter, r *http.Request) {
	var req model.ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	if err := h.service.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		switch {
		case errors.Is(err, service.ErrShortPassword):
			middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านต้องยาวอย่างน้อย 8 ตัว")
		case errors.Is(err, service.ErrBadResetToken):
			middleware.WriteError(w, http.StatusBadRequest, "ลิงก์นี้หมดอายุหรือถูกใช้ไปแล้ว — กรุณาขอลิงก์ใหม่")
		default:
			slog.Error("wbw reset password failed", "err", err)
			middleware.WriteError(w, http.StatusInternalServerError, "ตั้งรหัสผ่านใหม่ไม่สำเร็จ")
		}
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Capacity — GET /wbw/capacity · เปิดสาธารณะ หน้าสมัครเรียกก่อนแสดงฟอร์ม
//
// ตั้งใจไม่เอาไว้ใต้ /wbw/auth เพราะกลุ่มนั้นมี ThrottleBacklog (40 พร้อมกัน คิว 25 วิ)
// ไว้กัน bcrypt เผา CPU · endpoint นี้อ่านแถวเดียว ไม่ควรไปแย่งคิวกับการสมัครจริง
func (h *WBWAuthHandler) Capacity(w http.ResponseWriter, r *http.Request) {
	c, err := h.service.Capacity(r.Context())
	if err != nil {
		slog.Error("wbw capacity failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "อ่านจำนวนที่นั่งไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, c)
}
