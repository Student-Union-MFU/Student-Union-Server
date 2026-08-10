package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"su-server/internal/middleware"
	"su-server/internal/repository"
	"su-server/internal/service"
)

type WBWGroupHandler struct {
	service *service.WBWGroupService
}

func NewWBWGroupHandler(s *service.WBWGroupService) *WBWGroupHandler {
	return &WBWGroupHandler{service: s}
}

// Members GET /wbw/groups/{groupId}/members
func (h *WBWGroupHandler) Members(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupIDParam(r)
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "กลุ่มไม่ถูกต้อง")
		return
	}
	out, err := h.service.Members(r.Context(), groupID)
	if err != nil {
		slog.Error("list group members failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดสมาชิกไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}

// MembersIndex GET /wbw/groups/members/index
//
// ต้องลงทะเบียนก่อน /{groupId}/members ใน router ไม่งั้น chi จะจับ "members" เป็น groupId
func (h *WBWGroupHandler) MembersIndex(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.MembersIndex(r.Context())
	if err != nil {
		slog.Error("group members index failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายชื่อไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Join POST /wbw/groups/{groupId}/join
func (h *WBWGroupHandler) Join(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupIDParam(r)
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "กลุ่มไม่ถูกต้อง")
		return
	}
	uid, _ := actor(r)

	err := h.service.Join(r.Context(), uid, groupID)
	switch {
	case errors.Is(err, repository.ErrGroupFull):
		middleware.WriteError(w, http.StatusConflict, "กลุ่มเต็มแล้ว เลือกกลุ่มอื่น")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบกลุ่มนี้")
	case errors.Is(err, repository.ErrAlreadyInGroup):
		middleware.WriteError(w, http.StatusConflict, "ท่านอยู่ในกลุ่มอยู่แล้ว ต้องออกจากกลุ่มเดิมก่อน")
	case err != nil:
		slog.Error("join group failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "เข้ากลุ่มไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "group_id": groupID})
	}
}

// Leave POST /wbw/groups/leave
func (h *WBWGroupHandler) Leave(w http.ResponseWriter, r *http.Request) {
	uid, _ := actor(r)

	err := h.service.Leave(r.Context(), uid)
	switch {
	case errors.Is(err, repository.ErrNoGroup):
		// ออกจากกลุ่มทั้งที่ไม่ได้อยู่กลุ่มไหน — ผลลัพธ์ที่ต้องการเกิดขึ้นแล้ว ตอบสำเร็จ
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบข้อมูลผู้เข้าร่วม")
	case errors.Is(err, repository.ErrNoQuota):
		middleware.WriteError(w, http.StatusConflict, "สิทธิ์ออกจากกลุ่มหมดแล้ว")
	case err != nil:
		slog.Error("leave group failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ออกจากกลุ่มไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
