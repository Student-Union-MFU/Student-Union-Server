package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	appmw "su-server/internal/middleware"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

// Local alias so the handler reads without the service qualifier repeated.
type ErrShortOfThreshold = service.ErrPrizeThresholdNotReached

type ClubFairFairHandler struct {
	checkIns *service.ClubFairCheckInService
}

func NewClubFairFairHandler(checkIns *service.ClubFairCheckInService) *ClubFairFairHandler {
	return &ClubFairFairHandler{checkIns: checkIns}
}

// GET /clubfair/zones — the three areas, in signage order.
func (h *ClubFairFairHandler) ListZones(w http.ResponseWriter, r *http.Request) {
	zones, err := h.checkIns.ListZones(r.Context())
	if err != nil {
		slog.Error("failed to list zones", "err", err)
		appmw.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโหลดข้อมูลโซนได้")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, zones)
}

// GET /clubfair/progress — everything Home renders, in one call.
func (h *ClubFairFairHandler) Progress(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	progress, err := h.checkIns.Progress(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("failed to read progress", "err", err, "user", claims.UserID)
		appmw.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโหลดความคืบหน้าได้")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, progress)
}

// GET /clubfair/checkins — this student's stamps, oldest first.
func (h *ClubFairFairHandler) ListCheckIns(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	checkIns, err := h.checkIns.List(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("failed to list check-ins", "err", err, "user", claims.UserID)
		appmw.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโหลดประวัติการสแกนได้")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, checkIns)
}

// POST /clubfair/checkins — { payload, client_id, device_time }
//
// The response says which of the three things happened rather than only whether
// it worked, because the app has three different things to show someone standing
// at a booth. All three are 2xx: "you already collected this" is an answer, not
// a failure.
func (h *ClubFairFairHandler) CreateCheckIn(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	var body struct {
		// The scanned QR, verbatim. The server parses it — the client does not
		// get to tell us which booth it was.
		Payload string `json:"payload"`
		// Idempotency key, minted on the phone before the request goes out.
		ClientID   string `json:"client_id"`
		DeviceTime string `json:"device_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}
	if body.ClientID == "" {
		// Without it a retry cannot be told from a second scan, which is the one
		// thing this endpoint has to get right.
		appmw.WriteError(w, http.StatusBadRequest, "ต้องมี client_id")
		return
	}

	checkIn, outcome, err := h.checkIns.Record(
		r.Context(), claims.UserID, body.Payload, body.ClientID, body.DeviceTime,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCheckInCodeMalformed):
			appmw.WriteError(w, http.StatusBadRequest, "โค้ดนี้ไม่ใช่โค้ดของบูธในงาน")
		case errors.Is(err, service.ErrCheckInCodeInvalid):
			appmw.WriteError(w, http.StatusBadRequest, "โค้ดไม่ถูกต้อง")
		case errors.Is(err, service.ErrCheckInCodeExpired):
			// Its own message: the student did nothing wrong and re-scanning
			// fixes it, which "invalid" would not tell them.
			appmw.WriteError(w, http.StatusConflict, "โค้ดหมดอายุแล้ว กรุณาสแกนใหม่อีกครั้ง")
		case errors.Is(err, repository.ErrBoothNotFound):
			appmw.WriteError(w, http.StatusNotFound, "ไม่พบบูธนี้")
		default:
			slog.Error("failed to record check-in", "err", err, "user", claims.UserID)
			appmw.WriteError(w, http.StatusInternalServerError, "บันทึกการสแกนไม่สำเร็จ")
		}
		return
	}

	status := http.StatusCreated
	if outcome != service.CheckInRecorded {
		status = http.StatusOK
	}
	appmw.WriteJSON(w, status, map[string]any{
		"outcome":  outcome,
		"check_in": checkIn,
	})
}

// GET /clubfair/booths/{id}/checkin-code — staff, admin, or the booth's owner.
//
// What the device at a booth polls to know what to display. The secret stays on
// the server: a tablet left on a table at a fair is not somewhere to keep
// something that mints valid check-ins for the rest of the event.
//
// A booth owner reaches only the booths assigned to them, checked in the service
// against the token's own claims — see CurrentCode. The route's middleware
// cannot do that job: it is a question about this booth and this user, not about
// a role.
func (h *ClubFairFairHandler) BoothCheckInCode(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	boothID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || boothID <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "รหัสบูธไม่ถูกต้อง")
		return
	}

	code, err := h.checkIns.CurrentCode(r.Context(), boothID, claims.UserID, claims.Role)
	if err != nil {
		if errors.Is(err, service.ErrBoothNotOwned) {
			appmw.WriteError(w, http.StatusForbidden, "บูธนี้ไม่ได้อยู่ในความดูแลของคุณ")
			return
		}
		if errors.Is(err, repository.ErrBoothNotFound) {
			appmw.WriteError(w, http.StatusNotFound, "ไม่พบบูธนี้")
			return
		}
		slog.Error("failed to build check-in code", "err", err, "booth", boothID)
		appmw.WriteError(w, http.StatusInternalServerError, "ไม่สามารถสร้างโค้ดได้")
		return
	}
	appmw.WriteJSON(w, http.StatusOK, code)
}

// POST /clubfair/prizes/claim — staff only. { user_id, tier_id }
//
// Staff-authorised because a prize is a physical object leaving a table. The
// threshold is re-checked against the server's own count, not against whatever
// the staff device believes.
func (h *ClubFairFairHandler) ClaimPrize(w http.ResponseWriter, r *http.Request) {
	claims := appmw.ClubFairClaimsFrom(r.Context())

	var body struct {
		UserID int `json:"user_id"`
		TierID int `json:"tier_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		appmw.WriteError(w, http.StatusBadRequest, "รูปแบบคำขอไม่ถูกต้อง")
		return
	}
	if body.UserID <= 0 || body.TierID <= 0 {
		appmw.WriteError(w, http.StatusBadRequest, "ต้องมี user_id และ tier_id")
		return
	}

	claimed, err := h.checkIns.ClaimPrize(r.Context(), body.UserID, body.TierID, claims.UserID)
	if err != nil {
		slog.Warn("prize claim rejected", "err", err, "user", body.UserID, "tier", body.TierID)

		// Never err.Error() in the body: those messages are English, carry a
		// package prefix, and are written for a log rather than for someone
		// standing at a prize table.
		var short ErrShortOfThreshold
		if errors.As(err, &short) {
			appmw.WriteError(w, http.StatusConflict, fmt.Sprintf(
				"เก็บได้ %d บูธ ต้องครบ %d บูธก่อนรับรางวัล", short.Collected, short.Needed))
			return
		}
		appmw.WriteError(w, http.StatusConflict, "รับรางวัลนี้ไม่ได้")
		return
	}
	if !claimed {
		appmw.WriteError(w, http.StatusConflict, "รับรางวัลระดับนี้ไปแล้ว")
		return
	}
	appmw.WriteJSON(w, http.StatusCreated, map[string]bool{"claimed": true})
}
