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

type WBWFeedbackHandler struct {
	service *service.WBWFeedbackService
}

func NewWBWFeedbackHandler(s *service.WBWFeedbackService) *WBWFeedbackHandler {
	return &WBWFeedbackHandler{service: s}
}

// Submit POST /wbw/me/feedback — ผู้เข้าร่วมส่งความเห็นต่อฐานที่เช็คอินแล้ว
func (h *WBWFeedbackHandler) Submit(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	var req model.FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}

	saved, created, err := h.service.Submit(r.Context(), claims.Subject, req)
	switch {
	case err == nil:
		if created {
			middleware.WriteJSON(w, http.StatusCreated, saved)
		} else {
			// client_id เดิม = retry ตอนเน็ตหลุด ไม่ใช่ error
			middleware.WriteJSON(w, http.StatusOK, saved)
		}
	case errors.Is(err, service.ErrBadRating), errors.Is(err, service.ErrFeedbackMissingClientID):
		middleware.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, repository.ErrNotCheckedIn):
		middleware.WriteError(w, http.StatusForbidden, "ยังไม่ได้เช็คอินฐานนี้")
	default:
		var dup repository.ErrAlreadyAnswered
		if errors.As(err, &dup) {
			middleware.WriteJSON(w, http.StatusConflict, dup.Existing)
			return
		}
		slog.Error("submit feedback failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ส่งความเห็นไม่สำเร็จ")
	}
}

// AdminList GET /wbw/admin/feedback — ความเห็นทั้งหมด + สรุปต่อฐาน
func (h *WBWFeedbackHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.AdminList(r.Context())
	if err != nil {
		slog.Error("list feedback failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดความเห็นไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}
