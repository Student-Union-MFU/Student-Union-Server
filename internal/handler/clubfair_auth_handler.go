package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	appmw "su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"
)

type ClubFairAuthHandler struct {
	service *service.ClubFairAuthService
}

func NewClubFairAuthHandler(s *service.ClubFairAuthService) *ClubFairAuthHandler {
	return &ClubFairAuthHandler{service: s}
}

// authError maps a service error onto a status and a Thai message.
//
// Central so the mapping cannot drift between endpoints, and so an error nobody
// anticipated becomes a 500 with a generic body rather than leaking whatever the
// database said onto a student's screen.
func authError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrClubFairNoAccount),
		errors.Is(err, service.ErrClubFairWrongPassword):
		// One message for both. Distinguishing them turns this endpoint into a
		// way to discover which phone numbers have accounts.
		appmw.WriteError(w, http.StatusUnauthorized, "เบอร์โทรหรือรหัสผ่านไม่ถูกต้อง")

	case errors.Is(err, service.ErrClubFairNoPasswordSet):
		appmw.WriteError(w, http.StatusConflict,
			"บัญชีนี้ยังไม่ได้ตั้งรหัสผ่าน กรุณาเข้าสู่ระบบด้วย Google")

	case errors.Is(err, service.ErrClubFairAccountFlagged):
		appmw.WriteError(w, http.StatusForbidden, "บัญชีนี้ถูกระงับการใช้งาน")

	case errors.Is(err, service.ErrClubFairBadPhone):
		appmw.WriteError(w, http.StatusBadRequest, "กรอกเบอร์มือถือไทย เช่น 0683150329")

	case errors.Is(err, service.ErrClubFairWeakPassword):
		appmw.WriteError(w, http.StatusBadRequest,
			"รหัสผ่านต้องมีอย่างน้อย 8 ตัวอักษร และมีทั้งตัวอักษรและตัวเลข")

	case errors.Is(err, service.ErrClubFairEmailTaken):
		appmw.WriteError(w, http.StatusConflict, "อีเมลหรือเบอร์นี้มีบัญชีอยู่แล้ว")

	case errors.Is(err, service.ErrClubFairNotMFU):
		// 403, not 401: nothing about the caller's credentials is wrong, the
		// address simply is not eligible — and saying so is the only way they
		// can act on it.
		appmw.WriteError(w, http.StatusForbidden, "ใช้ได้เฉพาะอีเมล @lamduan.mfu.ac.th")

	case errors.Is(err, service.ErrClubFairNameRequired):
		appmw.WriteError(w, http.StatusBadRequest, "ต้องกรอกชื่อและนามสกุล")

	case errors.Is(err, repository.ErrClubFairUserNotFound):
		appmw.WriteError(w, http.StatusNotFound, "ไม่พบบัญชีนี้")

	case errors.Is(err, service.ErrClubFairTokensDisabled):
		slog.Error("clubfair auth reached with tokens disabled")
		appmw.WriteError(w, http.StatusServiceUnavailable, "ระบบเข้าสู่ระบบยังไม่พร้อมใช้งาน")

	default:
		// Everything from VerifyGoogleIdentity lands here — the MFU domain rule,
		// an unverified address, an audience mismatch. 401 with one message: the
		// caller does not need to know which check it failed.
		slog.Warn("clubfair auth rejected", "err", err)
		appmw.WriteError(w, http.StatusUnauthorized, "เข้าสู่ระบบไม่สำเร็จ")
	}
}

// POST /clubfair/auth/google — { id_token }
func (h *ClubFairAuthHandler) SignInWithGoogle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	session, err := h.service.SignInWithGoogle(r.Context(), body.IDToken)
	if err != nil {
		authError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, session)
}

// POST /clubfair/auth/login — { phone, password }
func (h *ClubFairAuthHandler) SignInWithPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	session, err := h.service.SignInWithPassword(r.Context(), body.Phone, body.Password)
	if err != nil {
		authError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, session)
}

// POST /clubfair/auth/register
func (h *ClubFairAuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FirstName string `json:"first_name"`
		Surname   string `json:"surname"`
		Email     string `json:"email"`
		Phone     string `json:"phone"`
		StudentID string `json:"student_id"`
		School    string `json:"school"`
		Major     string `json:"major"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	session, err := h.service.RegisterWithPassword(
		r.Context(),
		body.FirstName, body.Surname, body.Email, body.Phone,
		body.StudentID, body.School, body.Major, body.Password,
	)
	if err != nil {
		authError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, session)
}

// GET /clubfair/me
func (h *ClubFairAuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())
	user, err := h.service.GetByID(r.Context(), claims.UserID)
	if err != nil {
		authError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, model.NewPublicClubFairUser(*user))
}

// PATCH /clubfair/me — the fields the student owns. Absent fields are untouched.
func (h *ClubFairAuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	// Pointers, so "not in the request" is distinguishable from "set to empty".
	var body struct {
		Phone  *string `json:"phone"`
		School *string `json:"school"`
		Major  *string `json:"major"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	user, err := h.service.UpdateProfile(r.Context(), claims.UserID, body.Phone, body.School, body.Major)
	if err != nil {
		authError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, model.NewPublicClubFairUser(*user))
}

// PUT /clubfair/me/password — how a Google-only account gains the fallback login.
func (h *ClubFairAuthHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	if err := h.service.SetPassword(r.Context(), claims.UserID, body.Password); err != nil {
		authError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
