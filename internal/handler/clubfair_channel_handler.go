package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	appmw "su-server/internal/middleware"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

type ClubFairChannelHandler struct {
	service *service.ClubFairChannelService
}

func NewClubFairChannelHandler(s *service.ClubFairChannelService) *ClubFairChannelHandler {
	return &ClubFairChannelHandler{service: s}
}

// GET /clubfair/announcements — oldest first, with `mine` resolved for the caller.
//
// Authenticated even though the content is the same for everyone: `mine` on each
// reaction chip is per-student, and there is no anonymous way to answer it.
func (h *ClubFairChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	posts, err := h.service.List(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("failed to list announcements", "err", err)
		appmw.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโหลดประกาศได้")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, posts)
}

// POST /clubfair/announcements — staff only. { body }
func (h *ClubFairChannelHandler) Post(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	var payload struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	post, err := h.service.Post(r.Context(), claims.UserID, payload.Body)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmptyAnnouncement):
			appmw.WriteError(w, http.StatusBadRequest, "ต้องมีข้อความ")
		case errors.Is(err, service.ErrAnnouncementTooLong):
			appmw.WriteError(w, http.StatusBadRequest, "ข้อความยาวเกินไป")
		default:
			slog.Error("failed to post announcement", "err", err)
			appmw.WriteError(w, http.StatusInternalServerError, "ส่งประกาศไม่สำเร็จ")
		}
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, post)
}

// DELETE /clubfair/announcements/{id} — staff only. Soft delete.
func (h *ClubFairChannelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสประกาศไม่ถูกต้อง")
		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrAnnouncementNotFound) {
			appmw.WriteError(w, http.StatusNotFound, "ไม่พบประกาศนี้")
			return
		}
		slog.Error("failed to delete announcement", "err", err, "id", id)
		appmw.WriteError(w, http.StatusInternalServerError, "ลบประกาศไม่สำเร็จ")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /clubfair/announcements/{id}/reactions — { emoji }
//
// A toggle, and the response says which way it went so the client renders the
// result rather than predicting it.
func (h *ClubFairChannelHandler) React(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสประกาศไม่ถูกต้อง")
		return
	}

	var payload struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	reacted, err := h.service.React(r.Context(), id, claims.UserID, payload.Emoji)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUnsupportedReaction):
			appmw.WriteError(w, http.StatusBadRequest, "ใช้รีแอคชันนี้ไม่ได้")
		case errors.Is(err, repository.ErrAnnouncementNotFound):
			appmw.WriteError(w, http.StatusNotFound, "ไม่พบประกาศนี้")
		default:
			slog.Error("failed to toggle reaction", "err", err, "id", id)
			appmw.WriteError(w, http.StatusInternalServerError, "บันทึกรีแอคชันไม่สำเร็จ")
		}
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"mine": reacted})
}
