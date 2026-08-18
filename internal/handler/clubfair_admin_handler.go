package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	appmw "su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

// ClubFairAdminHandler is the staff dashboard's roster and prize tiers, plus the
// public list of tiers a student can still reach.
type ClubFairAdminHandler struct {
	service *service.ClubFairAdminService
}

func NewClubFairAdminHandler(s *service.ClubFairAdminService) *ClubFairAdminHandler {
	return &ClubFairAdminHandler{service: s}
}

// adminError maps the dashboard's rules onto statuses and Thai messages.
//
// The three participant rules answer 403 rather than 400: the request is
// well-formed and the caller is authenticated, they simply may not do this. A
// 400 would read as "you typed it wrong" for something no retyping fixes.
func adminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrClubFairRoleUnknown):
		appmw.WriteError(w, http.StatusBadRequest, "ไม่ใช่สิทธิ์ที่มีอยู่")
	case errors.Is(err, service.ErrClubFairNotAdmin):
		appmw.WriteError(w, http.StatusForbidden, "เฉพาะผู้ดูแลระบบเท่านั้นที่เปลี่ยนสิทธิ์ได้")
	case errors.Is(err, service.ErrClubFairSelfEdit):
		appmw.WriteError(w, http.StatusForbidden, "เปลี่ยนบัญชีของตัวเองที่นี่ไม่ได้")
	case errors.Is(err, service.ErrClubFairLastAdmin):
		appmw.WriteError(w, http.StatusConflict, "นี่คือผู้ดูแลระบบคนสุดท้าย")
	case errors.Is(err, repository.ErrClubFairUserNotFound),
		errors.Is(err, repository.ErrClubFairParticipantNotFound):
		appmw.WriteError(w, http.StatusNotFound, "ไม่พบบัญชีนี้")
	case errors.Is(err, service.ErrClubFairEmailRequired):
		appmw.WriteError(w, http.StatusBadRequest, "ต้องใช้อีเมล @lamduan.mfu.ac.th")
	case errors.Is(err, service.ErrClubFairNameRequired):
		appmw.WriteError(w, http.StatusBadRequest, "ต้องกรอกชื่อและนามสกุล")
	case errors.Is(err, service.ErrClubFairBadPhone):
		appmw.WriteError(w, http.StatusBadRequest, "เบอร์โทรไม่ถูกต้อง")
	case errors.Is(err, service.ErrClubFairWeakPassword):
		appmw.WriteError(w, http.StatusBadRequest,
			"รหัสผ่านต้องยาวอย่างน้อย 8 ตัว และมีทั้งตัวอักษรและตัวเลข")
	case errors.Is(err, repository.ErrClubFairAccountTaken):
		appmw.WriteError(w, http.StatusConflict,
			"อีเมล เบอร์โทร หรือรหัสนักศึกษานี้มีบัญชีอยู่แล้ว")
	case errors.Is(err, service.ErrClubFairNotBoothOwner):
		appmw.WriteError(w, http.StatusConflict,
			"กำหนดบูธได้เฉพาะบัญชีที่มีสิทธิ์ผู้ดูแลบูธเท่านั้น")
	case errors.Is(err, repository.ErrBoothNotFound):
		appmw.WriteError(w, http.StatusNotFound, "ไม่พบบูธนี้")

	case errors.Is(err, service.ErrPrizeNameRequired):
		appmw.WriteError(w, http.StatusBadRequest, "ต้องมีชื่อรางวัล")
	case errors.Is(err, service.ErrPrizeThresholdRange):
		appmw.WriteError(w, http.StatusBadRequest, "จำนวนบูธต้องอย่างน้อย 1 บูธ")
	case errors.Is(err, repository.ErrPrizeThresholdTaken):
		appmw.WriteError(w, http.StatusConflict, "มีรางวัลที่ใช้จำนวนบูธนี้อยู่แล้ว")
	case errors.Is(err, repository.ErrPrizeTierHasClaims):
		// Its own message, and its own action: the tier cannot go, but it can be
		// retired. Telling staff only that the delete failed would leave them
		// with no way forward.
		appmw.WriteError(w, http.StatusConflict,
			"มีนักศึกษารับรางวัลระดับนี้ไปแล้ว ปิดการใช้งานแทนการลบ")
	case errors.Is(err, repository.ErrPrizeTierNotFound):
		appmw.WriteError(w, http.StatusNotFound, "ไม่พบรางวัลระดับนี้")

	default:
		slog.Error("clubfair admin request failed", "err", err)
		appmw.WriteError(w, http.StatusInternalServerError, "ดำเนินการไม่สำเร็จ")
	}
}

// ---- Participants --------------------------------------------------------

// GET /clubfair/admin/participants?q=&limit=&offset= — staff only.
func (h *ClubFairAdminHandler) ListParticipants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// Unparseable values become 0 and the service substitutes its own defaults,
	// rather than answering 400: a dashboard that sends `limit=` on first load
	// wants the first page, not an error.
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	/*
	 * ?role=staff,admin,booth_owner narrows the list, and an absent or empty
	 * parameter means everyone. Split here rather than accepting repeated
	 * `role=` parameters because the dashboard builds this from a fixed set of
	 * tabs and one comma-joined value is the shorter thing to read in a log.
	 *
	 * Unknown names are passed through rather than rejected: the filter is a
	 * WHERE clause, so a role that does not exist simply matches nothing, and a
	 * 400 here would be a second place that has to learn about a new role.
	 */
	var roles []string
	for _, role := range strings.Split(q.Get("role"), ",") {
		if trimmed := strings.TrimSpace(role); trimmed != "" {
			roles = append(roles, trimmed)
		}
	}

	participants, total, err := h.service.ListParticipants(r.Context(), q.Get("q"), roles, limit, offset)
	if err != nil {
		adminError(w, err)
		return
	}
	// An envelope rather than a bare array, because a page of results without
	// the total is a list the dashboard cannot paginate.
	appmw.WriteJSON(w, http.StatusOK, map[string]any{
		"participants": participants,
		"total":        total,
	})
}

// PATCH /clubfair/admin/participants/{id} — staff only; role changes are admin only.
//
// A patch, not a replace: these are the two fields staff may touch, and an
// absent one means "leave it". Everything else about a student is theirs.
func (h *ClubFairAdminHandler) UpdateParticipant(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Pointers, so "not in the request" is distinguishable from "set to false".
	var body struct {
		Role      *string `json:"role"`
		IsFlagged *bool   `json:"is_flagged"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	// The actor comes off the token, never off the body — the role rule is only
	// worth anything if the client it restricts cannot assert its own role.
	participant, err := h.service.UpdateParticipant(
		r.Context(), id, body.Role, body.IsFlagged, claims.UserID, claims.Role,
	)
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, participant)
}

// ---- Prize tiers ---------------------------------------------------------

// GET /clubfair/prizes — the tiers a student can still reach. Public.
//
// This is what stops a client hardcoding the thresholds. The app reads them off
// `/clubfair/progress` because it has a token and wants them alongside the
// student's own count; the website has neither, and used to hold a copy of the
// numbers in its own source with a comment apologising for it.
func (h *ClubFairAdminHandler) ListPrizes(w http.ResponseWriter, r *http.Request) {
	tiers, err := h.service.ActivePrizeTiers(r.Context())
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, tiers)
}

// GET /clubfair/admin/prizes — retired tiers and claim counts included. Staff only.
func (h *ClubFairAdminHandler) ListPrizesForAdmin(w http.ResponseWriter, r *http.Request) {
	tiers, err := h.service.PrizeTiersForAdmin(r.Context())
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, tiers)
}

type prizeTierBody struct {
	Threshold   int     `json:"threshold"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	// Only read on update. A new tier is always active — creating one retired
	// would be creating a row for nothing.
	IsActive bool `json:"is_active"`
}

// POST /clubfair/admin/prizes — staff only.
func (h *ClubFairAdminHandler) CreatePrize(w http.ResponseWriter, r *http.Request) {
	var body prizeTierBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	tier, err := h.service.CreatePrizeTier(r.Context(), body.Threshold, body.Name, body.Description)
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, tier)
}

// PUT /clubfair/admin/prizes/{id} — staff only.
func (h *ClubFairAdminHandler) UpdatePrize(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var body prizeTierBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	tier, err := h.service.UpdatePrizeTier(
		r.Context(), id, body.Threshold, body.Name, body.Description, body.IsActive)
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, tier)
}

// DELETE /clubfair/admin/prizes/{id} — staff only, and only for a tier nobody
// has collected. See ErrPrizeTierHasClaims.
func (h *ClubFairAdminHandler) DeletePrize(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeletePrizeTier(r.Context(), id); err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- Booth writes --------------------------------------------------------
//
// On this handler rather than on BoothHandler, which serves two products: its
// GetAllBooths is registered under both /su-server/booths and /clubfair/booths.
// Editing a booth is a Club Fair staff action, and hanging it off the shared
// handler would put a Club Fair-authorised write next to an SU-authorised read.

// ClubFairBoothHandler carries the write half of the booth table.
type ClubFairBoothHandler struct {
	service *service.BoothService
}

func NewClubFairBoothHandler(s *service.BoothService) *ClubFairBoothHandler {
	return &ClubFairBoothHandler{service: s}
}

func boothError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrBoothNameRequired):
		appmw.WriteError(w, http.StatusBadRequest, "ต้องมีชื่อชมรม")
	case errors.Is(err, service.ErrBoothCategoryUnknown):
		appmw.WriteError(w, http.StatusBadRequest, "ไม่ใช่ประเภทชมรมที่มีอยู่")
	case errors.Is(err, service.ErrBoothCodeZoneMismatch):
		appmw.WriteError(w, http.StatusBadRequest, "รหัสบูธต้องขึ้นต้นด้วยตัวอักษรของโซน")
	case errors.Is(err, repository.ErrBoothCodeTaken):
		appmw.WriteError(w, http.StatusConflict, "มีบูธที่ใช้รหัสนี้อยู่แล้ว")
	case errors.Is(err, repository.ErrBoothHasCheckIns):
		// The one that needs its consequence spelled out: deleting this booth
		// would cascade away every stamp collected at it, which can push
		// students back under a prize they had already reached.
		appmw.WriteError(w, http.StatusConflict,
			"มีนักศึกษาสแกนบูธนี้ไปแล้ว ลบไม่ได้เพราะแสตมป์จะหายไปด้วย")
	case errors.Is(err, repository.ErrBoothNotFound):
		appmw.WriteError(w, http.StatusNotFound, "ไม่พบบูธนี้")
	default:
		slog.Error("clubfair booth write failed", "err", err)
		appmw.WriteError(w, http.StatusInternalServerError, "ดำเนินการไม่สำเร็จ")
	}
}

// boothBody is a booth as the dashboard sends it.
//
// There is no `secret` field and there must never be one: a booth's HMAC key is
// minted by the column's DEFAULT at insert time and never leaves the database.
// An endpoint that accepted one would be an endpoint for setting a booth's key
// to a value the caller already knows.
type boothBody struct {
	Name      string  `json:"name"`
	NameEN    *string `json:"name_en"`
	Category  string  `json:"category"`
	Zone      *string `json:"zone"`
	BoothCode *string `json:"booth_code"`
	About     *string `json:"about"`
	Icon      *string `json:"icon"`
}

func (b boothBody) booth() model.PublicBooth {
	return model.PublicBooth{
		Name:      b.Name,
		NameEN:    b.NameEN,
		Category:  b.Category,
		Zone:      b.Zone,
		BoothCode: b.BoothCode,
		About:     b.About,
		Icon:      b.Icon,
	}
}

// GET /clubfair/admin/booth-categories — the five values the CHECK allows.
//
// Served rather than hardcoded in the dashboard because adding a sixth needs a
// migration, and a form offering a value the column will refuse is a form whose
// submit fails with a constraint violation.
func (h *ClubFairBoothHandler) Categories(w http.ResponseWriter, r *http.Request) {
	appmw.WriteJSON(w, http.StatusOK, service.BoothCategories)
}

// POST /clubfair/admin/booths — staff only.
func (h *ClubFairBoothHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body boothBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	booth, err := h.service.CreateBooth(r.Context(), body.booth())
	if err != nil {
		boothError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, booth)
}

// PUT /clubfair/admin/booths/{id} — staff only. A whole-row replace.
func (h *ClubFairBoothHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var body boothBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	booth, err := h.service.UpdateBooth(r.Context(), id, body.booth())
	if err != nil {
		boothError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, booth)
}

// DELETE /clubfair/admin/booths/{id} — staff only, and only for a booth nobody
// has scanned. See ErrBoothHasCheckIns.
func (h *ClubFairBoothHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteBooth(r.Context(), id); err != nil {
		boothError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /clubfair/admin/participants — staff for students, admin for any role.
//
// The one path that creates a Club Fair account without its owner present. It
// exists for the people running the fair rather than for students: a booth owner
// has to be a row before they can be assigned a booth. See
// ClubFairAdminService.CreateParticipant for what it refuses to relax.
//
// The password is in the request and never in a response. It goes to bcrypt and
// nowhere else — the created account comes back as a ClubFairParticipant, which
// has no credential field to leak.
func (h *ClubFairAdminHandler) CreateParticipant(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	var body struct {
		FirstName string  `json:"first_name"`
		Surname   string  `json:"surname"`
		Email     string  `json:"email"`
		Phone     *string `json:"phone"`
		StudentID *string `json:"student_id"`
		School    *string `json:"school"`
		Major     *string `json:"major"`
		Role      string  `json:"role"`
		Password  string  `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	created, err := h.service.CreateParticipant(r.Context(), service.ClubFairNewParticipant{
		FirstName: body.FirstName,
		Surname:   body.Surname,
		Email:     body.Email,
		Phone:     body.Phone,
		StudentID: body.StudentID,
		School:    body.School,
		Major:     body.Major,
		Role:      body.Role,
		Password:  body.Password,
	}, claims.Role)
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, created)
}

// GET /clubfair/admin/participants/{id} — staff only.
//
// One person, with the booths they run. The assignment travels with the row
// rather than on an endpoint of its own because every screen that wants one
// wants the other: the roster's detail panel shows both, and two requests to
// fill one panel is two chances for it to render half-populated.
func (h *ClubFairAdminHandler) ParticipantDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสบัญชีไม่ถูกต้อง")
		return
	}

	person, err := h.service.Participant(r.Context(), id)
	if err != nil {
		adminError(w, err)
		return
	}

	boothIDs, err := h.service.OwnedBoothIDs(r.Context(), id)
	if err != nil {
		adminError(w, err)
		return
	}

	appmw.WriteJSON(w, http.StatusOK, map[string]any{
		"participant": person,
		"booth_ids":   boothIDs,
	})
}

// PUT /clubfair/admin/participants/{id}/booths — staff only.
//
// Replaces the whole assignment set. A PUT rather than a POST/DELETE pair
// because the dashboard edits this as a row of checkboxes: sending the intended
// set means the last writer wins on a screen someone is looking at, where a diff
// computed in the browser would be a diff against a list that may have moved.
func (h *ClubFairAdminHandler) SetParticipantBooths(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสบัญชีไม่ถูกต้อง")
		return
	}

	var body struct {
		BoothIDs []int `json:"booth_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	if err := h.service.SetOwnedBooths(r.Context(), id, body.BoothIDs, claims.UserID); err != nil {
		adminError(w, err)
		return
	}

	// The stored set read back, not the set that was sent. They are the same
	// unless something deduplicated — and a dashboard that renders what it hoped
	// it wrote is how a checkbox ends up disagreeing with the database.
	boothIDs, err := h.service.OwnedBoothIDs(r.Context(), id)
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]any{"booth_ids": boothIDs})
}

// GET /clubfair/me/booths — any signed-in account.
//
// The booths the caller runs. This is what the booth owner's own screen loads,
// and it needs no role check of its own: an account with no assignments gets an
// empty list, which is the true answer for every student in the fair.
func (h *ClubFairAdminHandler) MyBooths(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	booths, err := h.service.OwnedBooths(r.Context(), claims.UserID)
	if err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, booths)
}

// PUT /clubfair/admin/participants/{id}/password — admin only.
//
// The recovery path for an account whose owner cannot sign in. See
// ClubFairAdminService.SetParticipantPassword for why it is admin-only, why it
// refuses your own account, and why it does **not** end a session already
// running.
//
// The new password is in the request and in no response. It goes to bcrypt and
// nowhere else; the reply is an acknowledgement, because echoing a credential
// back is how one ends up in a log.
func (h *ClubFairAdminHandler) SetParticipantPassword(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสบัญชีไม่ถูกต้อง")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}

	if err := h.service.SetParticipantPassword(
		r.Context(), id, body.Password, claims.UserID, claims.Role,
	); err != nil {
		adminError(w, err)
		return
	}
	appmw.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
