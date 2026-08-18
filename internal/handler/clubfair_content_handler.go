package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	appmw "su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"
	"time"

	"github.com/go-chi/chi/v5"
)

// ClubFairContentHandler serves the fair's own details and its running order.
//
// Both are read publicly and written by staff, which is why one handler carries
// both halves — the two routes for the programme differ only in whether drafts
// are included, and putting them in one file is what keeps that difference
// visible.
type ClubFairContentHandler struct {
	service *service.ClubFairContentService
}

func NewClubFairContentHandler(s *service.ClubFairContentService) *ClubFairContentHandler {
	return &ClubFairContentHandler{service: s}
}

// contentError maps the service's rules onto a status and a Thai message.
//
// Central for the same reason `authError` is: the mapping cannot drift between
// four endpoints, and an error nobody anticipated becomes a 500 with a generic
// body rather than putting whatever the database said on a staff screen.
func contentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrFairWindowBackwards):
		appmw.WriteError(w, http.StatusBadRequest, "เวลาสิ้นสุดต้องอยู่หลังเวลาเริ่ม")
	case errors.Is(err, service.ErrProgramWindowBackwards):
		appmw.WriteError(w, http.StatusBadRequest, "เวลาสิ้นสุดของรายการต้องอยู่หลังเวลาเริ่ม")
	case errors.Is(err, service.ErrProgramTitleMissing):
		appmw.WriteError(w, http.StatusBadRequest, "ต้องมีชื่อรายการ")
	case errors.Is(err, repository.ErrProgramEntryNotFound):
		appmw.WriteError(w, http.StatusNotFound, "ไม่พบรายการนี้")
	case errors.Is(err, repository.ErrFairInfoMissing):
		// Not a client error: the migration seeds this row, so its absence means
		// the database has been edited by hand.
		slog.Error("clubfair_fair_info row is missing")
		appmw.WriteError(w, http.StatusInternalServerError, "ยังไม่มีข้อมูลงาน")
	default:
		slog.Error("clubfair content request failed", "err", err)
		appmw.WriteError(w, http.StatusInternalServerError, "ดำเนินการไม่สำเร็จ")
	}
}

// GET /clubfair/info — when and where the fair is. Public.
//
// Public because it is the first question anyone deciding whether to come asks,
// and because it is what replaces the copy of these dates that the app and the
// website each used to hold.
func (h *ClubFairContentHandler) Info(w http.ResponseWriter, r *http.Request) {
	info, err := h.service.FairInfo(r.Context())
	if err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, info)
}

// fairInfoBody is the shape the dashboard PUTs.
//
// `starts_at` and `ends_at` are RFC 3339 with an offset, decoded straight into
// time.Time. The dashboard sends the instant it built from a date picker in
// campus time; nothing here re-interprets it, because a server that guesses a
// timezone for a wall-clock string is how an event ends up starting at 09:00 UTC.
type fairInfoBody struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Venue    *string   `json:"venue"`
	VenueEN  *string   `json:"venue_en"`
	Notice   *string   `json:"notice"`
	NoticeEN *string   `json:"notice_en"`
}

// PUT /clubfair/admin/info — staff only.
func (h *ClubFairContentHandler) SaveInfo(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	var body fairInfoBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}
	if body.StartsAt.IsZero() || body.EndsAt.IsZero() {
		// A missing or unparseable timestamp decodes to the zero time, which
		// would otherwise be stored as the year 1 and read by every client as a
		// fair that ended two thousand years ago.
		appmw.WriteError(w, http.StatusBadRequest, "ต้องระบุเวลาเริ่มและเวลาสิ้นสุด")
		return
	}

	info, err := h.service.SaveFairInfo(
		r.Context(), body.StartsAt, body.EndsAt,
		body.Venue, body.VenueEN, body.Notice, body.NoticeEN,
		claims.UserID,
	)
	if err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, info)
}

// GET /clubfair/program — the published running order. Public.
func (h *ClubFairContentHandler) Program(w http.ResponseWriter, r *http.Request) {
	entries, err := h.service.PublishedProgram(r.Context())
	if err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, entries)
}

// GET /clubfair/admin/program — drafts included. Staff only.
func (h *ClubFairContentHandler) ProgramForAdmin(w http.ResponseWriter, r *http.Request) {
	entries, err := h.service.FullProgram(r.Context())
	if err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, entries)
}

// programBody is one entry as the dashboard sends it.
//
// Every field is present on every write — see UpdateProgramEntry in the
// repository for why this is a whole-row replace rather than a patch.
type programBody struct {
	StartsAt   time.Time  `json:"starts_at"`
	EndsAt     *time.Time `json:"ends_at"`
	Title      string     `json:"title"`
	TitleEN    *string    `json:"title_en"`
	Detail     *string    `json:"detail"`
	DetailEN   *string    `json:"detail_en"`
	Location   *string    `json:"location"`
	LocationEN *string    `json:"location_en"`
	Zone       *string    `json:"zone"`
	// Absent means draft. A create that forgot the field should not publish.
	IsPublished bool `json:"is_published"`
}

func (b programBody) entry() model.ClubFairProgramEntry {
	return model.ClubFairProgramEntry{
		StartsAt:    b.StartsAt,
		EndsAt:      b.EndsAt,
		Title:       b.Title,
		TitleEN:     b.TitleEN,
		Detail:      b.Detail,
		DetailEN:    b.DetailEN,
		Location:    b.Location,
		LocationEN:  b.LocationEN,
		Zone:        b.Zone,
		IsPublished: b.IsPublished,
	}
}

// decodeProgramBody reads and sanity-checks the one field JSON cannot.
func decodeProgramBody(w http.ResponseWriter, r *http.Request) (*programBody, bool) {
	var body programBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return nil, false
	}
	if body.StartsAt.IsZero() {
		appmw.WriteError(w, http.StatusBadRequest, "ต้องระบุเวลาเริ่มของรายการ")
		return nil, false
	}
	return &body, true
}

// POST /clubfair/admin/program — staff only.
func (h *ClubFairContentHandler) CreateProgramEntry(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeProgramBody(w, r)
	if !ok {
		return
	}

	entry, err := h.service.CreateProgramEntry(r.Context(), body.entry())
	if err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, entry)
}

// PUT /clubfair/admin/program/{id} — staff only. A whole-row replace.
func (h *ClubFairContentHandler) UpdateProgramEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	body, ok := decodeProgramBody(w, r)
	if !ok {
		return
	}

	entry, err := h.service.UpdateProgramEntry(r.Context(), id, body.entry())
	if err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, entry)
}

// DELETE /clubfair/admin/program/{id} — staff only.
func (h *ClubFairContentHandler) DeleteProgramEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteProgramEntry(r.Context(), id); err != nil {
		contentError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// pathID reads a positive {id} from the path, or answers 400 and reports false.
//
// Shared by every Club Fair admin route that takes one. It exists because the
// alternative is the same six lines in eleven handlers, and the one that gets
// them subtly wrong accepts `id = 0` — which no row has, so it becomes a 404
// that looks like missing data rather than a malformed request.
func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสไม่ถูกต้อง")
		return 0, false
	}
	return id, true
}
