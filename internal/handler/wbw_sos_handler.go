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

type WBWSOSHandler struct {
	service *service.WBWSOSService
}

func NewWBWSOSHandler(s *service.WBWSOSService) *WBWSOSHandler { return &WBWSOSHandler{service: s} }

// sosIDParam อ่าน {id} จาก path · เลขเคสผิดรูปถือเป็น 400 ไม่ใช่ 500
func sosIDParam(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// Raise POST /wbw/me/sos
func (h *WBWSOSHandler) Raise(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	var req model.SOSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}

	c, created, err := h.service.Raise(r.Context(), claims.Subject, req)
	switch {
	case err == nil && created:
		middleware.WriteJSON(w, http.StatusCreated, c)
	case err == nil:
		// client_id เดิม หรือมีเคสเปิดอยู่แล้ว (ย้ำ) — ไม่ใช่ error
		middleware.WriteJSON(w, http.StatusOK, c)
	case errors.Is(err, service.ErrSOSMissingClientID):
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ครบ")
	default:
		slog.Error("เปิดเคส SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ส่งขอความช่วยเหลือไม่สำเร็จ")
	}
}

// Cancel POST /wbw/me/sos/{id}/cancel
func (h *WBWSOSHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	id, err := sosIDParam(r)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขเคสไม่ถูกต้อง")
		return
	}

	switch err := h.service.Cancel(r.Context(), claims.Subject, id); {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, repository.ErrSOSAlreadyAcked):
		middleware.WriteError(w, http.StatusConflict, "เจ้าหน้าที่รับเรื่องแล้ว ให้โทรบอกแทน")
	case errors.Is(err, repository.ErrSOSTooLateToCancel):
		middleware.WriteError(w, http.StatusConflict, "เลยเวลายกเลิกแล้ว ให้โทรบอกแทน")
	case errors.Is(err, repository.ErrSOSNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบเคสนี้")
	default:
		slog.Error("ยกเลิกเคส SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ยกเลิกไม่สำเร็จ")
	}
}

// Active GET /wbw/me/sos/active?wait=<0-25>
//
// ไม่มีเคสเปิดอยู่ไม่ใช่ error — ตอบ 200 พร้อม body เป็น null ให้แอปแยกจาก "โหลดไม่ขึ้น"
func (h *WBWSOSHandler) Active(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait")) // อ่านไม่ออกถือเป็น 0 (poll ธรรมดา) เหมือน Sync

	c, err := h.service.Active(r.Context(), claims.Subject, service.ClampWait(wait))
	if err != nil {
		slog.Error("อ่านเคส SOS ที่เปิดอยู่ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "อ่านเคสไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, c)
}

// Get GET /wbw/me/sos/{id} — คนกดเองหรือเพื่อนในกลุ่มเดียวกัน
func (h *WBWSOSHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	id, err := sosIDParam(r)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขเคสไม่ถูกต้อง")
		return
	}

	c, err := h.service.Get(r.Context(), claims.Subject, id)
	switch {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, c)
	case errors.Is(err, repository.ErrSOSNotFound):
		// ครอบทั้ง "ไม่มีเคสนี้" และ "มีแต่ไม่มีสิทธิ์เห็น" — คืนแบบเดียวกันตั้งใจ ไม่รั่วว่ามีเคสอยู่
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบเคสนี้")
	default:
		slog.Error("อ่านเคส SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "อ่านเคสไม่สำเร็จ")
	}
}

// StaffFeed GET /wbw/staff/sos?since=<cursor>&wait=<0-25>
func (h *WBWSOSHandler) StaffFeed(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	since := r.URL.Query().Get("since")
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))

	list, err := h.service.StaffFeed(r.Context(), claims.Subject, claims.Role, since, service.ClampWait(wait))
	if err != nil {
		slog.Error("โหลดรายการเคส SOS ของเจ้าหน้าที่ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายการไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Ack POST /wbw/staff/sos/{id}/ack — "กำลังไป" · คนที่สองไม่ถือเป็น error (ดู service.Ack)
func (h *WBWSOSHandler) Ack(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	id, err := sosIDParam(r)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขเคสไม่ถูกต้อง")
		return
	}

	c, err := h.service.Ack(r.Context(), claims.Subject, claims.Role, id)
	switch {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, c)
	case errors.Is(err, repository.ErrSOSNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบเคสนี้")
	default:
		slog.Error("รับเรื่อง SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "รับเรื่องไม่สำเร็จ")
	}
}

// Resolve POST /wbw/staff/sos/{id}/resolve
// Report POST /wbw/staff/sos/{id}/report — เจ้าหน้าที่รายงานผลหลังไปถึงเคส
//
// body: {"outcome": "false_alarm" | "minor" | "major" | "urgent"}
//
// false_alarm/minor ปิดเคส · major/urgent ไม่ปิด แค่ยกระดับ — ตัวตัดสินอยู่ใน service
// ไม่ใช่ที่นี่และไม่ใช่ที่แอป
func (h *WBWSOSHandler) Report(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	id, err := sosIDParam(r)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขเคสไม่ถูกต้อง")
		return
	}
	var req model.SOSReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}

	switch err := h.service.Report(r.Context(), claims.Subject, claims.Role, id, req.Outcome); {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, service.ErrSOSBadReason):
		middleware.WriteError(w, http.StatusBadRequest, "ผลการรายงานไม่ถูกต้อง")
	case errors.Is(err, repository.ErrSOSNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบเคสนี้")
	default:
		slog.Error("รายงานผลเคส SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "รายงานผลไม่สำเร็จ")
	}
}

func (h *WBWSOSHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	id, err := sosIDParam(r)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "เลขเคสไม่ถูกต้อง")
		return
	}
	var req model.SOSResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}

	switch err := h.service.Resolve(r.Context(), claims.Subject, claims.Role, id, req.Reason); {
	case err == nil:
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, service.ErrSOSBadReason):
		middleware.WriteError(w, http.StatusBadRequest, "เหตุผลไม่ถูกต้อง")
	case errors.Is(err, repository.ErrSOSNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบเคสนี้")
	default:
		slog.Error("ปิดเคส SOS ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ปิดเคสไม่สำเร็จ")
	}
}
