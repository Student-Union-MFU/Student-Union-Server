package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/service"
)

type WBWChatHandler struct {
	service *service.WBWChatService
}

func NewWBWChatHandler(s *service.WBWChatService) *WBWChatHandler {
	return &WBWChatHandler{service: s}
}

// groupIDParam อ่าน {groupId} · ปฏิเสธค่าที่ใหญ่เกิน int ตั้งแต่ชั้น HTTP
// ไม่งั้นจะกลายเป็น cast error ที่ DB ซึ่งตอบ 500 ทั้งที่เป็นความผิดของ input
func groupIDParam(r *http.Request) (int, bool) {
	id, err := intParam(r, "groupId")
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// parseAfter อ่าน ?after= · ต้องเป็นเลขล้วน และไม่ยาวเกินที่ bigint รับไหว
//
// ตัด 0 นำหน้าก่อนวัดความยาว กัน "000...01" (ค่าจริงเล็กนิดเดียวแต่สตริงยาว) โดนปัดตก
// ยาวเกิน 18 หลัก = เกิน bigint (~9.2e18) ปฏิเสธด้วย 400 ดีกว่าปล่อยให้ overflow ที่ DB
func parseAfter(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	trimmed := strings.TrimLeft(raw, "0")
	if trimmed == "" {
		return 0, true // "0" หรือ "0000"
	}
	if len(trimmed) > 18 {
		return 0, false
	}
	v, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// writeChatErr แปลง error ของ service เป็น status ที่ตรงความหมาย
// คืน true เมื่อจัดการแล้ว (handler ต้อง return ต่อ)
func writeChatErr(w http.ResponseWriter, err error, failMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, service.ErrNotGroupMember):
		middleware.WriteError(w, http.StatusForbidden, "ต้องอยู่ในกลุ่มนี้ก่อน")
	case errors.Is(err, service.ErrBadLastReadID):
		middleware.WriteError(w, http.StatusBadRequest, "last_read_id ไม่ถูกต้อง")
	case errors.Is(err, service.ErrMissingClientID):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี client_id")
	case errors.Is(err, service.ErrEmptyBody):
		middleware.WriteError(w, http.StatusBadRequest, "ข้อความว่าง")
	case errors.Is(err, service.ErrBodyTooLong):
		middleware.WriteError(w, http.StatusBadRequest, "ข้อความยาวเกินไป")
	case errors.Is(err, service.ErrClientIDTaken):
		middleware.WriteError(w, http.StatusConflict, "client_id นี้ถูกใช้ไปแล้ว")
	case errors.Is(err, service.ErrBadClientID):
		middleware.WriteError(w, http.StatusBadRequest, "client_id ไม่ถูกต้อง")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// client หลุดกลางคัน — ไม่มีใครรอรับคำตอบแล้ว ไม่ต้องเขียน response
		slog.Debug("chat: client หลุดกลางคัน")
	default:
		slog.Error(failMsg, "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, failMsg)
	}
	return true
}

// Sync GET /wbw/groups/{groupId}/chat/sync?after=<id>&wait=<0-25>
func (h *WBWChatHandler) Sync(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupIDParam(r)
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "กลุ่มไม่ถูกต้อง")
		return
	}
	afterID, ok := parseAfter(r.URL.Query().Get("after"))
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "after ไม่ถูกต้อง")
		return
	}
	// wait ที่อ่านไม่ออกถือเป็น 0 (poll ธรรมดา) ไม่ใช่ 400 — พารามิเตอร์นี้เป็น hint ไม่ใช่ข้อมูล
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))

	uid, _ := actor(r)
	out, err := h.service.Sync(r.Context(), uid, groupID, afterID, service.ClampWait(wait))
	if writeChatErr(w, err, "ซิงก์แชทไม่สำเร็จ") {
		return
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}

// Read POST /wbw/groups/{groupId}/chat/read
func (h *WBWChatHandler) Read(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupIDParam(r)
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "กลุ่มไม่ถูกต้อง")
		return
	}
	var req model.ChatReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "last_read_id ไม่ถูกต้อง")
		return
	}

	uid, _ := actor(r)
	if writeChatErr(w, h.service.MarkRead(r.Context(), uid, groupID, req), "บันทึกสถานะอ่านไม่สำเร็จ") {
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Messages GET /wbw/groups/{groupId}/messages?after=<id>&limit=<n>
func (h *WBWChatHandler) Messages(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupIDParam(r)
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "กลุ่มไม่ถูกต้อง")
		return
	}

	var afterPtr *int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		v, ok := parseAfter(raw)
		if !ok {
			middleware.WriteError(w, http.StatusBadRequest, "after ไม่ถูกต้อง")
			return
		}
		afterPtr = &v
	}

	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, 100)
	}

	uid, _ := actor(r)
	list, err := h.service.Messages(r.Context(), uid, groupID, afterPtr, limit)
	if writeChatErr(w, err, "โหลดข้อความไม่สำเร็จ") {
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Send POST /wbw/groups/{groupId}/messages
func (h *WBWChatHandler) Send(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupIDParam(r)
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "กลุ่มไม่ถูกต้อง")
		return
	}
	var req model.SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	uid, _ := actor(r)
	msg, err := h.service.Send(r.Context(), uid, groupID, req)
	if writeChatErr(w, err, "ส่งข้อความไม่สำเร็จ") {
		return
	}
	middleware.WriteJSON(w, http.StatusCreated, msg)
}
