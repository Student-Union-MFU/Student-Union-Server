package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

type WBWNotificationHandler struct {
	service *service.WBWNotificationService
	admin   *service.WBWAdminService // ใช้บันทึก audit log
}

func NewWBWNotificationHandler(s *service.WBWNotificationService, admin *service.WBWAdminService) *WBWNotificationHandler {
	return &WBWNotificationHandler{service: s, admin: admin}
}

// audienceLabel แปลง audience เป็นข้อความไทยสำหรับ audit log
func audienceLabel(audience *string) string {
	if audience == nil {
		return "ทุกคน"
	}
	switch *audience {
	case "group":
		return "รายกลุ่ม"
	case "school":
		return "รายสำนักวิชา"
	case "user":
		return "รายบุคคล"
	default:
		return "ทุกคน"
	}
}

func (h *WBWNotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.NotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี title")
		return
	}
	uid, uname := actor(r)

	n, err := h.service.Create(r.Context(), req, uid)
	switch {
	case errors.Is(err, service.ErrMissingFields):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี title")
	case err != nil:
		slog.Error("create notification failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ส่งประกาศไม่สำเร็จ")
	default:
		h.admin.Log(r.Context(), uid, uname, "ส่งประกาศ", n.Title+" · "+audienceLabel(req.Audience))
		// ⚠ ของเดิมยิง FCM push ต่อจากตรงนี้ — ยังไม่ได้ทำในฝั่ง Go
		middleware.WriteJSON(w, http.StatusCreated, n)
	}
}

func (h *WBWNotificationHandler) ListSent(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListSent(r.Context())
	if err != nil {
		slog.Error("list sent notifications failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

func (h *WBWNotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	uid, _ := actor(r)
	list, err := h.service.ListForUser(r.Context(), uid)
	if err != nil {
		slog.Error("list notifications failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// MarkRead POST /wbw/notifications/{id}/read — ผู้ใช้กดอ่านประกาศ
func (h *WBWNotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "ประกาศไม่ถูกต้อง")
		return
	}
	uid, _ := actor(r)

	if err := h.service.MarkRead(r.Context(), uid, id); err != nil {
		slog.Error("mark notification read failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "บันทึกสถานะอ่านไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ListPublic — ประกาศสาธารณะ (audience='all') · เปิดดูได้โดยไม่ต้องล็อกอิน
func (h *WBWNotificationHandler) ListPublic(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListPublic(r.Context())
	if err != nil {
		slog.Error("list public notifications failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

/* ---------- draft ---------- */

func (h *WBWNotificationHandler) GetDraft(w http.ResponseWriter, r *http.Request) {
	uid, _ := actor(r)
	d, err := h.service.GetDraft(r.Context(), uid)
	if err != nil {
		slog.Error("get draft failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	// ยังไม่มี draft → ตอบ null (frontend รับได้)
	middleware.WriteJSON(w, http.StatusOK, d)
}

func (h *WBWNotificationHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	var req model.PresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	uid, _ := actor(r)

	d, err := h.service.SaveDraft(r.Context(), uid, req)
	if err != nil {
		slog.Error("save draft failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "บันทึกไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, d)
}

func (h *WBWNotificationHandler) DeleteDraft(w http.ResponseWriter, r *http.Request) {
	uid, _ := actor(r)
	if err := h.service.DeleteDraft(r.Context(), uid); err != nil {
		slog.Error("delete draft failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลบไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- presets ---------- */

func (h *WBWNotificationHandler) ListPresets(w http.ResponseWriter, r *http.Request) {
	uid, _ := actor(r)
	list, err := h.service.ListPresets(r.Context(), uid)
	if err != nil {
		slog.Error("list presets failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

func (h *WBWNotificationHandler) CreatePreset(w http.ResponseWriter, r *http.Request) {
	var req model.PresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องตั้งชื่อ preset")
		return
	}
	uid, _ := actor(r)

	p, err := h.service.CreatePreset(r.Context(), uid, req)
	switch {
	case errors.Is(err, service.ErrEmptyName):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องตั้งชื่อ preset")
	case err != nil:
		slog.Error("create preset failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "บันทึกไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusCreated, p)
	}
}

func (h *WBWNotificationHandler) DeletePreset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
		return
	}
	uid, _ := actor(r)

	if err := h.service.DeletePreset(r.Context(), uid, id); err != nil {
		slog.Error("delete preset failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลบไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
