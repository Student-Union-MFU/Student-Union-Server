package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

// รายชื่อสำนักวิชาแทบไม่เปลี่ยน แต่หน้าสมัครเรียกทุกครั้งที่โหลด (คนเข้าหลักพันพร้อมกัน)
// cache ในหน่วยความจำ TTL สั้น ๆ → 2000 requests เหมือนกันเหลือ query DB จริงครั้งเดียว
var schoolsCache struct {
	mu      sync.RWMutex
	data    []model.School
	expires time.Time
}

const schoolsCacheTTL = 5 * time.Minute

type WBWAdminHandler struct {
	service *service.WBWAdminService
}

func NewWBWAdminHandler(s *service.WBWAdminService) *WBWAdminHandler {
	return &WBWAdminHandler{service: s}
}

// actor ดึงตัวตนผู้เรียกไว้บันทึก audit
func actor(r *http.Request) (id, name string) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		return "", ""
	}
	return claims.Subject, claims.Username
}

// intParam อ่าน path param ที่เป็นตัวเลข
func intParam(r *http.Request, key string) (int, error) {
	return strconv.Atoi(chi.URLParam(r, key))
}

/* ---------- schools / dashboard / groups / logs ---------- */

func (h *WBWAdminHandler) ListSchools(w http.ResponseWriter, r *http.Request) {
	// Cache-Control ให้ CDN/เบราว์เซอร์ cache ต่อได้อีกชั้น (ย้ายขึ้น Cloudflare ทีหลัง)
	w.Header().Set("Cache-Control", "public, max-age=300")

	// อ่านจาก cache ในหน่วยความจำก่อน (กัน DB โดนถล่มด้วย query เดียวกันหลักพันครั้ง)
	schoolsCache.mu.RLock()
	cached, fresh := schoolsCache.data, time.Now().Before(schoolsCache.expires)
	schoolsCache.mu.RUnlock()
	if fresh && cached != nil {
		middleware.WriteJSON(w, http.StatusOK, cached)
		return
	}

	schools, err := h.service.ListSchools(r.Context())
	if err != nil {
		// DB ล่ม แต่มี cache เก่าค้างอยู่ → เสิร์ฟของเก่าไปก่อน ดีกว่าพัง
		if cached != nil {
			middleware.WriteJSON(w, http.StatusOK, cached)
			return
		}
		slog.Error("list schools failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายชื่อสำนักวิชาไม่ได้")
		return
	}

	schoolsCache.mu.Lock()
	schoolsCache.data = schools
	schoolsCache.expires = time.Now().Add(schoolsCacheTTL)
	schoolsCache.mu.Unlock()

	middleware.WriteJSON(w, http.StatusOK, schools)
}

func (h *WBWAdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.Dashboard(r.Context())
	if err != nil {
		slog.Error("dashboard failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, stats)
}

func (h *WBWAdminHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.service.ListGroups(r.Context())
	if err != nil {
		slog.Error("list groups failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, groups)
}

func (h *WBWAdminHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := h.service.ListLogs(r.Context())
	if err != nil {
		slog.Error("list logs failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, logs)
}

/* ---------- participants ---------- */

func (h *WBWAdminHandler) ListParticipants(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListParticipants(r.Context())
	if err != nil {
		slog.Error("list participants failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายชื่อไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// Me — โปรไฟล์ของ "ผู้เข้าร่วมที่ล็อกอินอยู่" (อ่านของตัวเองเท่านั้น)
//
// ใช้ query เดียวกับ ParticipantDetail ของ admin แต่ล็อก id ไว้ที่ sub ของ token
// จึงดึงได้แค่ของตัวเอง · เปิดให้ทุกคนที่ล็อกอิน (requireAuth) ไม่ใช่เฉพาะ admin
// query กรอง role = 'participant' อยู่แล้ว staff/admin เรียกจะได้ 404 (ใช้ /dashboard แทน)
func (h *WBWAdminHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	d, err := h.service.ParticipantDetail(r.Context(), claims.Subject)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบข้อมูลผู้เข้าร่วม")
	case err != nil:
		slog.Error("me detail failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusOK, d)
	}
}

// PatchMe PATCH /wbw/me — ผู้เข้าร่วมแก้รูปโปรไฟล์ตัวเอง
//
// รับเฉพาะ photo_url · ไม่มี key นี้ใน body = ไม่มีอะไรให้แก้ ตอบ 400 แทนที่จะลบรูปทิ้ง
// (ส่ง "photo_url": null มาโดยตั้งใจ = ลบรูป ซึ่งต่างจากไม่ส่ง key มาเลย จึงต้องแยกสองกรณีนี้)
func (h *WBWAdminHandler) PatchMe(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	field, ok := raw["photo_url"]
	if !ok {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องมี photo_url")
		return
	}
	var photoURL *string
	if err := json.Unmarshal(field, &photoURL); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "photo_url ไม่ถูกต้อง")
		return
	}

	err := h.service.UpdateOwnPhoto(r.Context(), claims.Subject, photoURL)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบข้อมูลผู้เข้าร่วม")
	case err != nil:
		slog.Error("update own photo failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "บันทึกรูปไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

/*
DeleteMe — ผู้เข้าร่วมลบบัญชีของตัวเอง (DELETE /wbw/me)

หน้า /privacy ของเว็บและหน้าตั้งค่าของแอปเรียกตัวนี้ · เป็น "ลบทันที" ไม่ใช่คำขอ
ที่รอแอดมินอนุมัติ เพราะทั้ง App Store และ Google Play บังคับว่าแอปที่สร้างบัญชีได้
ต้องลบบัญชีได้เองในทางที่ไม่ต้องรอใคร (ทรงเดียวกับ DELETE /clubfair/me)

ตัวยืนยันตัวตนคือ token — เว็บบังคับให้ล็อกอินใหม่ก่อนถึงจะกดปุ่มนี้ได้อยู่แล้ว
จึงเท่ากับยืนยันรหัสผ่านไปในตัว ไม่ต้องรับรหัสผ่านซ้ำใน body

⚠ เจ้าหน้าที่/แอดมินลบตัวเองทางนี้ไม่ได้: แถวของเขาถูกอ้างจาก checkpoint_staff,
sos_event.acked_by และประกาศที่เขาเป็นคนส่ง ซึ่งทรานแซกชันลบผู้เข้าร่วมไม่ได้เคลียร์ให้
· repository กรอง role = 'participant' อยู่แล้วแต่จะตอบเป็น 404 ซึ่งอ่านแล้วงง
ตรงนี้จึงดู claims.Role ก่อนแล้วตอบ 403 พร้อมบอกว่าให้ติดต่อผู้ดูแลแทน
*/
func (h *WBWAdminHandler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		middleware.WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
		return
	}
	if claims.Role != "participant" {
		middleware.WriteError(w, http.StatusForbidden,
			"บัญชีเจ้าหน้าที่ลบเองไม่ได้ กรุณาติดต่อผู้ดูแลระบบ")
		return
	}

	// บันทึก admin_log เขียนอยู่ในทรานแซกชันของ repository แล้ว — ที่นี่จึงไม่เรียก
	// h.service.Log ตามหลังเหมือน DeleteParticipant ของแอดมิน (ตอนนั้นแถวผู้ใช้หายไปแล้ว
	// actor_id จะชน FK แล้วบันทึกหายเงียบ · ดูคำอธิบายที่ DeleteOwnAccount)
	_, err := h.service.DeleteOwnAccount(r.Context(), claims.Subject, claims.Username)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		// token ยังไม่หมดอายุแต่บัญชีถูกลบไปแล้ว (แอดมินลบให้ หรือกดสองแท็บพร้อมกัน)
		// — ปลายทางที่ผู้ใช้ต้องการคือ "ไม่มีบัญชีนี้แล้ว" ซึ่งเป็นจริงอยู่ ตอบสำเร็จไป
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case err != nil:
		slog.Error("delete own account failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลบบัญชีไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (h *WBWAdminHandler) ParticipantDetail(w http.ResponseWriter, r *http.Request) {
	d, err := h.service.ParticipantDetail(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบผู้เข้าร่วม")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("participant detail failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลไม่สำเร็จ")
	default:
		middleware.WriteJSON(w, http.StatusOK, d)
	}
}

func (h *WBWAdminHandler) UpdateParticipant(w http.ResponseWriter, r *http.Request) {
	var patch model.ParticipantPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	id := chi.URLParam(r, "id")

	aid, aname := actor(r)
	p, err := h.service.UpdateParticipant(r.Context(), id, patch, aid)
	switch {
	case errors.Is(err, service.ErrBadStudentID):
		middleware.WriteError(w, http.StatusBadRequest, "รหัสนักศึกษาต้อง 10 หลัก ขึ้นต้น 693")
	case errors.Is(err, service.ErrBadQuota):
		middleware.WriteError(w, http.StatusBadRequest, "สิทธิ์ออกจากกลุ่มต้องอยู่ระหว่าง 0 ถึง 10")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบผู้สมัคร")
	case repository.IsPGCode(err, "23505"):
		middleware.WriteError(w, http.StatusConflict, "รหัสนักศึกษาซ้ำ")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("update participant failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "แก้ไขไม่สำเร็จ")
	default:
		h.service.Log(r.Context(), aid, aname, "แก้ไขผู้เข้าร่วม", derefStr(p.StudentID))
		// แยก log เป็นแถวของตัวเอง เฉพาะตอนมีการปรับโควตาจริง ไม่งั้นหน้า Logs จะไม่เห็นรายการนี้
		if patch.LeaveQuota != nil {
			h.service.Log(r.Context(), aid, aname, "ปรับสิทธิ์ออกกลุ่ม",
				fmt.Sprintf("%s → %d ครั้ง", derefStr(p.StudentID), *patch.LeaveQuota))
		}
		middleware.WriteJSON(w, http.StatusOK, p)
	}
}

func (h *WBWAdminHandler) ResetParticipantPassword(w http.ResponseWriter, r *http.Request) {
	var req model.PasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านอย่างน้อย 8 ตัว")
		return
	}

	studentID, err := h.service.ResetParticipantPassword(r.Context(), chi.URLParam(r, "id"), req.Password)
	switch {
	case errors.Is(err, service.ErrShortPassword):
		middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านอย่างน้อย 8 ตัว")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบผู้เข้าร่วม")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("reset participant password failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "รีเซ็ตรหัสไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "รีเซ็ตรหัสผ่านผู้เข้าร่วม", studentID)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (h *WBWAdminHandler) DeleteParticipant(w http.ResponseWriter, r *http.Request) {
	studentID, err := h.service.DeleteParticipant(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบผู้เข้าร่วม")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("delete participant failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลบไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "ลบผู้เข้าร่วม", studentID)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

/* ---------- checkpoints ---------- */

func (h *WBWAdminHandler) ListCheckpoints(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListCheckpoints(r.Context())
	if err != nil {
		slog.Error("list checkpoints failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลฐานไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

func (h *WBWAdminHandler) BasesOverview(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.BasesOverview(r.Context())
	if err != nil {
		slog.Error("bases overview failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดข้อมูลฐานไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

func (h *WBWAdminHandler) CreateCheckpoint(w http.ResponseWriter, r *http.Request) {
	var req model.CheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "กรุณากรอกชื่อฐาน")
		return
	}

	c, err := h.service.CreateCheckpoint(r.Context(), req)
	switch {
	case errors.Is(err, service.ErrEmptyName):
		middleware.WriteError(w, http.StatusBadRequest, "กรุณากรอกชื่อฐาน")
	case err != nil:
		slog.Error("create checkpoint failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "เพิ่มฐานไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "เพิ่มฐาน", c.Name)
		middleware.WriteJSON(w, http.StatusCreated, c)
	}
}

func (h *WBWAdminHandler) UpdateCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	var req model.CheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	c, err := h.service.UpdateCheckpoint(r.Context(), id, req)
	switch {
	case errors.Is(err, service.ErrEmptyName):
		middleware.WriteError(w, http.StatusBadRequest, "ชื่อฐานห้ามว่าง")
	case errors.Is(err, service.ErrBadCheckpoint):
		middleware.WriteError(w, http.StatusBadRequest, "ประเภทไม่ถูกต้อง")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบฐาน")
	case err != nil:
		slog.Error("update checkpoint failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "แก้ไขไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "แก้ไขฐาน", c.Name)
		middleware.WriteJSON(w, http.StatusOK, c)
	}
}

func (h *WBWAdminHandler) DeleteCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
		return
	}

	name, err := h.service.DeleteCheckpoint(r.Context(), id)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบฐาน")
	case repository.IsPGCode(err, "23503"):
		middleware.WriteError(w, http.StatusBadRequest, "ฐานนี้มีการเช็คอินแล้ว ลบไม่ได้")
	case err != nil:
		slog.Error("delete checkpoint failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลบฐานไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "ลบฐาน", name)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// GroupStaff GET /wbw/admin/groups/{id}/staff — ใครประจำกลุ่มนี้
func (h *WBWAdminHandler) GroupStaff(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	list, err := h.service.GroupStaff(r.Context(), id)
	if err != nil {
		slog.Error("list group staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายชื่อไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, list)
}

// AssignGroupStaff POST /wbw/admin/groups/{id}/staff — มอบหมายเจ้าหน้าที่ประจำกลุ่ม
//
// คนที่ถูกมอบหมายคือคนที่จะเห็น SOS ขั้นแรกของกลุ่มนี้ ก่อนที่จะยกระดับให้ทั้งงานเห็น
func (h *WBWAdminHandler) AssignGroupStaff(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	var req model.AssignStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องระบุเจ้าหน้าที่")
		return
	}
	username, err := h.service.AssignGroupStaff(r.Context(), id, req.UserID)
	switch {
	case errors.Is(err, service.ErrMissingFields):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องระบุเจ้าหน้าที่")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusBadRequest, "ไม่พบเจ้าหน้าที่")
	case repository.IsPGCode(err, "22P02"), repository.IsPGCode(err, "23503"):
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
	case err != nil:
		slog.Error("assign group staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "มอบหมายไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "มอบหมายเจ้าหน้าที่ประจำกลุ่ม", username)
		middleware.WriteJSON(w, http.StatusCreated, map[string]bool{"ok": true})
	}
}

// RemoveGroupStaff DELETE /wbw/admin/groups/{id}/staff/{userId}
func (h *WBWAdminHandler) RemoveGroupStaff(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	userID := chi.URLParam(r, "userId")
	ok, err := h.service.RemoveGroupStaff(r.Context(), id, userID)
	switch {
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
	case err != nil:
		slog.Error("remove group staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ถอนการมอบหมายไม่สำเร็จ")
	case !ok:
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบการมอบหมายนี้")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "ถอนเจ้าหน้าที่ประจำกลุ่ม", userID)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (h *WBWAdminHandler) AssignStaff(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	var req model.AssignStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ต้องระบุเจ้าหน้าที่")
		return
	}

	username, err := h.service.AssignStaff(r.Context(), id, req.UserID)
	switch {
	case errors.Is(err, service.ErrMissingFields):
		middleware.WriteError(w, http.StatusBadRequest, "ต้องระบุเจ้าหน้าที่")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusBadRequest, "ไม่พบเจ้าหน้าที่")
	case repository.IsPGCode(err, "22P02"), repository.IsPGCode(err, "23503"):
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
	case err != nil:
		slog.Error("assign staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "มอบหมายไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "มอบหมายเจ้าหน้าที่", username)
		middleware.WriteJSON(w, http.StatusCreated, map[string]bool{"ok": true})
	}
}

func (h *WBWAdminHandler) RemoveStaff(w http.ResponseWriter, r *http.Request) {
	id, err := intParam(r, "id")
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	userID := chi.URLParam(r, "userId")

	removed, err := h.service.RemoveStaff(r.Context(), id, userID)
	if err != nil && !repository.IsPGCode(err, "22P02") {
		slog.Error("remove staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ถอดเจ้าหน้าที่ไม่สำเร็จ")
		return
	}
	if removed {
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "ถอดเจ้าหน้าที่", userID)
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- staff / admin accounts ---------- */

func (h *WBWAdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.ListUsers(r.Context())
	if err != nil {
		slog.Error("list users failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดรายชื่อไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, users)
}

func (h *WBWAdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req model.CreateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "กรุณากรอกชื่อผู้ใช้")
		return
	}

	u, err := h.service.CreateUser(r.Context(), req)
	switch {
	case errors.Is(err, service.ErrMissingFields):
		middleware.WriteError(w, http.StatusBadRequest, "กรุณากรอกชื่อผู้ใช้")
	case errors.Is(err, service.ErrShortPassword):
		middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านอย่างน้อย 8 ตัว")
	case errors.Is(err, service.ErrBadRole):
		middleware.WriteError(w, http.StatusBadRequest, "บทบาทไม่ถูกต้อง")
	case errors.Is(err, service.ErrDuplicateUser):
		middleware.WriteError(w, http.StatusConflict, "ชื่อผู้ใช้นี้มีอยู่แล้ว")
	case err != nil:
		slog.Error("create user failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "สร้างบัญชีไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "สร้างบัญชี", u.Username)
		middleware.WriteJSON(w, http.StatusCreated, u)
	}
}

func (h *WBWAdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	var req model.UpdateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}
	callerID, aname := actor(r)
	id := chi.URLParam(r, "id")

	u, err := h.service.UpdateUser(r.Context(), id, callerID, req)
	switch {
	case errors.Is(err, service.ErrBadRole):
		middleware.WriteError(w, http.StatusBadRequest, "บทบาทไม่ถูกต้อง")
	case errors.Is(err, service.ErrSelfRole):
		middleware.WriteError(w, http.StatusBadRequest, "เปลี่ยนบทบาทของตัวเองไม่ได้")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบบัญชี")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("update user failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "แก้ไขไม่สำเร็จ")
	default:
		h.service.Log(r.Context(), callerID, aname, "แก้ไขบัญชี", u.Username)
		middleware.WriteJSON(w, http.StatusOK, u)
	}
}

func (h *WBWAdminHandler) SetUserPassword(w http.ResponseWriter, r *http.Request) {
	var req model.PasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านอย่างน้อย 8 ตัว")
		return
	}
	id := chi.URLParam(r, "id")

	err := h.service.SetUserPassword(r.Context(), id, req.Password)
	switch {
	case errors.Is(err, service.ErrShortPassword):
		middleware.WriteError(w, http.StatusBadRequest, "รหัสผ่านอย่างน้อย 8 ตัว")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบบัญชี")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("set user password failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "เปลี่ยนรหัสผ่านไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "เปลี่ยนรหัสผ่าน", id)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (h *WBWAdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	callerID, aname := actor(r)
	id := chi.URLParam(r, "id")

	username, err := h.service.DeleteUser(r.Context(), id, callerID)
	switch {
	case errors.Is(err, service.ErrSelfDelete):
		middleware.WriteError(w, http.StatusBadRequest, "ลบบัญชีตัวเองไม่ได้")
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบบัญชี")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("delete user failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ลบบัญชีไม่สำเร็จ")
	default:
		h.service.Log(r.Context(), callerID, aname, "ลบบัญชี", username)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

/* ---------- staff requests (สมัครเอง รออนุมัติ) ---------- */

func (h *WBWAdminHandler) ListStaffRequests(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.service.ListStaffRequests(r.Context())
	if err != nil {
		slog.Error("list staff requests failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "โหลดคำขอไม่สำเร็จ")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, reqs)
}

func (h *WBWAdminHandler) ApproveStaff(w http.ResponseWriter, r *http.Request) {
	username, err := h.service.ApproveStaff(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบคำขอ (อาจถูกดำเนินการไปแล้ว)")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("approve staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "อนุมัติไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "อนุมัติเจ้าหน้าที่", username)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (h *WBWAdminHandler) RejectStaff(w http.ResponseWriter, r *http.Request) {
	username, err := h.service.RejectStaff(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, repository.ErrNotFound):
		middleware.WriteError(w, http.StatusNotFound, "ไม่พบคำขอ (อาจถูกดำเนินการไปแล้ว)")
	case repository.IsPGCode(err, "22P02"):
		middleware.WriteError(w, http.StatusBadRequest, "id ไม่ถูกต้อง")
	case err != nil:
		slog.Error("reject staff failed", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ปฏิเสธไม่สำเร็จ")
	default:
		aid, aname := actor(r)
		h.service.Log(r.Context(), aid, aname, "ปฏิเสธคำขอเจ้าหน้าที่", username)
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
