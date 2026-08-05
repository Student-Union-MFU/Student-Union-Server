package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

/*
สัญญาของ status code ที่ฝั่งแอปตัดสินใจต่างกันสิ้นเชิงตามค่าที่ได้:
  201 = เคสใหม่ · 200 = ซ้ำหรือย้ำ · 400 = ข้อมูลผิดรูป · 401 = ไม่ได้ล็อกอิน
  409 = ยกเลิกไม่ได้เพราะรับเรื่องแล้ว · 404 = ไม่มีเคสนี้หรือไม่มีสิทธิ์เห็น

เดินผ่าน middleware.RequireAuth ของจริงด้วย token ที่เซ็นจริง เหมือน feedback —
เคส 401 จึงเป็นของจริง ไม่ใช่การยัด claims เข้า context เอง

ตัวปลอมคือ repository ไม่ใช่ service — service ของจริงยังตรวจ client_id และ reason
ให้ครบ มีแค่ชั้นที่คุยกับ Postgres ที่ถูกแทน (เหตุผลเดียวกับ fakeFeedbackRepo)
*/

const testSOSParticipantID = "11111111-1111-1111-1111-111111111111"
const testSOSStaffID = "22222222-2222-2222-2222-222222222222"

// stubSOSRepo — เหมือน fakeSOSRepo ใน service test แต่ตั้งค่าผลลัพธ์ต่อเคสได้
type stubSOSRepo struct {
	raised    *model.SOSCase
	created   bool
	raiseErr  error
	cancelErr error
}

func (s *stubSOSRepo) Checkpoints(context.Context) ([]repository.CheckpointGeo, error) {
	return nil, nil
}
func (s *stubSOSRepo) LastCheckinCheckpoint(context.Context, string) (*int, error) { return nil, nil }
func (s *stubSOSRepo) Raise(context.Context, string, model.SOSRequest, *int, *string) (*model.SOSCase, bool, error) {
	return s.raised, s.created, s.raiseErr
}
func (s *stubSOSRepo) Get(context.Context, int64) (*model.SOSCase, error) { return s.raised, nil }
func (s *stubSOSRepo) GetForViewer(context.Context, string, int64) (*model.SOSCase, error) {
	return s.raised, nil
}
func (s *stubSOSRepo) ActiveFor(context.Context, string) (*model.SOSCase, error) {
	return s.raised, nil
}
func (s *stubSOSRepo) Cancel(context.Context, string, int64) error          { return s.cancelErr }
func (s *stubSOSRepo) Ack(context.Context, int64, string) error             { return nil }
func (s *stubSOSRepo) Resolve(context.Context, int64, string, string) error { return nil }
func (s *stubSOSRepo) StaffFeed(context.Context, string, string, string) ([]model.SOSStaffCase, error) {
	return []model.SOSStaffCase{}, nil
}
func (s *stubSOSRepo) CanStaffSee(context.Context, string, string, int64) (bool, error) {
	return true, nil
}
func (s *stubSOSRepo) PushAudience(context.Context, int64) (*repository.SOSAudience, error) {
	return &repository.SOSAudience{}, nil
}
func (s *stubSOSRepo) PushAllowed(context.Context, int64) (bool, error) { return true, nil }
func (s *stubSOSRepo) MarkPushed(context.Context, int64) error          { return nil }

// newSOSTestRouter ผูก route ชุดเดียวกับที่ cmd/main.go ประกาศให้ SOS เข้ากับ router
// จริง — requireAuth/requireStaff เป็น middleware.RequireAuth/RequireRole ของจริง
// ไม่ใช่ตัวปลอมที่ยัด claims เข้า context เอง เพื่อให้เคส 401/403 เป็นของจริง
func newSOSTestRouter(t *testing.T, h *WBWSOSHandler) chi.Router {
	t.Helper()
	tokens := service.NewWBWTokenService()
	requireAuth := middleware.RequireAuth(tokens)
	requireStaff := middleware.RequireRole("admin", "staff")

	r := chi.NewRouter()
	r.Route("/wbw", func(r chi.Router) {
		r.With(requireAuth).Post("/me/sos", h.Raise)
		r.With(requireAuth).Get("/me/sos/active", h.Active)
		r.With(requireAuth).Get("/me/sos/{id}", h.Get)
		r.With(requireAuth).Post("/me/sos/{id}/cancel", h.Cancel)

		r.Route("/staff", func(r chi.Router) {
			r.Use(requireAuth, requireStaff)
			r.Get("/sos", h.StaffFeed)
			r.Post("/sos/{id}/ack", h.Ack)
			r.Post("/sos/{id}/resolve", h.Resolve)
		})
	})
	return r
}

// signTestToken เซ็น token จริงด้วย secret เดียวกับที่ newSOSTestRouter ใช้ตรวจ —
// ทั้งคู่อ่าน JWT_SECRET (หรือ default เดียวกัน) ผ่าน NewWBWTokenService คนละตัว
// แยกกัน จึงไม่ต้องแชร์ตัวแปรกันเพื่อให้ secret ตรงกัน (เหมือน postFeedback ที่ใช้
// tokens ตัวเดียวกันทำทั้งสองอย่าง — ผลลัพธ์เดียวกัน)
func signTestToken(t *testing.T, subject, role string) string {
	t.Helper()
	tok, err := service.NewWBWTokenService().Sign(subject, role, "test")
	if err != nil {
		t.Fatalf("เซ็น token ไม่สำเร็จ: %v", err)
	}
	return tok
}

// callSOS — ยิงคำขอจริงผ่าน RequireAuth → handler
// authorized=false = ไม่ใส่ header Authorization (คัดลอกรูปแบบจาก wbw_feedback_handler_test.go)
func callSOS(t *testing.T, repo *stubSOSRepo, method, path string, body any, authorized bool) *httptest.ResponseRecorder {
	t.Helper()
	h := NewWBWSOSHandler(service.NewWBWSOSService(repo, service.NewSOSEvents(nil, nil), nil, nil, "053-916-000"))
	router := newSOSTestRouter(t, h) // ผูก route เดียวกับ cmd/main.go ผ่าน RequireAuth ของจริง

	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if authorized {
		req.Header.Set("Authorization", "Bearer "+signTestToken(t, testSOSParticipantID, "participant"))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// callSOSAsStaff — callSOS ที่ผูก token บทบาท staff แทน participant (ไม่ผ่าน
// requireStaff ไม่ได้ถ้าใช้ token ของ callSOS ปกติ)
func callSOSAsStaff(t *testing.T, repo *stubSOSRepo, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	h := NewWBWSOSHandler(service.NewWBWSOSService(repo, service.NewSOSEvents(nil, nil), nil, nil, "053-916-000"))
	router := newSOSTestRouter(t, h)

	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+signTestToken(t, testSOSStaffID, "staff"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func sampleCase() *model.SOSCase {
	return &model.SOSCase{ID: 7, CreatedAt: "2026-08-06T10:00:00Z"}
}

func TestRaiseReturns201ForANewCase(t *testing.T) {
	rec := callSOS(t, &stubSOSRepo{raised: sampleCase(), created: true},
		http.MethodPost, "/wbw/me/sos",
		map[string]any{"client_id": "c1", "device_time": "2026-08-06T10:00:00Z"}, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("อยากได้ 201 ได้ %d body=%s", rec.Code, rec.Body)
	}
}

func TestRaiseReturns200WhenTheCaseAlreadyExists(t *testing.T) {
	// created=false ครอบทั้ง "client_id เดิม" และ "กดซ้ำระหว่างเคสเปิดอยู่" — ทั้งคู่ไม่ใช่ error
	rec := callSOS(t, &stubSOSRepo{raised: sampleCase(), created: false},
		http.MethodPost, "/wbw/me/sos",
		map[string]any{"client_id": "c1", "device_time": "2026-08-06T10:00:00Z"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("อยากได้ 200 ได้ %d", rec.Code)
	}
}

func TestRaiseReturns400WhenClientIDIsMissing(t *testing.T) {
	rec := callSOS(t, &stubSOSRepo{}, http.MethodPost, "/wbw/me/sos",
		map[string]any{"device_time": "2026-08-06T10:00:00Z"}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("อยากได้ 400 ได้ %d", rec.Code)
	}
}

func TestRaiseReturns401WithoutAToken(t *testing.T) {
	rec := callSOS(t, &stubSOSRepo{}, http.MethodPost, "/wbw/me/sos",
		map[string]any{"client_id": "c1", "device_time": "2026-08-06T10:00:00Z"}, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("อยากได้ 401 ได้ %d", rec.Code)
	}
}

func TestCancelReturns409OnceAcked(t *testing.T) {
	rec := callSOS(t, &stubSOSRepo{cancelErr: repository.ErrSOSAlreadyAcked},
		http.MethodPost, "/wbw/me/sos/7/cancel", nil, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("อยากได้ 409 ได้ %d", rec.Code)
	}
}

func TestCancelReturns409WhenTheWindowHasPassed(t *testing.T) {
	rec := callSOS(t, &stubSOSRepo{cancelErr: repository.ErrSOSTooLateToCancel},
		http.MethodPost, "/wbw/me/sos/7/cancel", nil, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("อยากได้ 409 ได้ %d", rec.Code)
	}
}

func TestResolveReturns400OnAnUnknownReason(t *testing.T) {
	rec := callSOSAsStaff(t, &stubSOSRepo{}, http.MethodPost, "/wbw/staff/sos/7/resolve",
		map[string]any{"reason": "เพราะอยาก"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("อยากได้ 400 ได้ %d", rec.Code)
	}
}

func TestStaffFeedRejectsAParticipantToken(t *testing.T) {
	// /wbw/staff/* อยู่หลัง requireStaff — participant ต้องไม่หลุดเข้าไปเห็นเคสของคนอื่น
	rec := callSOS(t, &stubSOSRepo{}, http.MethodGet, "/wbw/staff/sos", nil, true)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Fatalf("participant ต้องเข้าไม่ได้ ได้ %d", rec.Code)
	}
}

// สัญญาข้ามรีโป: ฝั่ง iOS แยก 409 จริงของเราออกจาก 409 ของ Cloudflare ด้วยรูปร่าง body
// (APIClient.sosIsOriginEnvelope) ไม่ใช่ด้วย status — ถ้ารูปนี้เปลี่ยน แอปจะเชื่อ WAF
func TestErrorBodiesAreTheOriginEnvelopeShape(t *testing.T) {
	cases := []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"400", callSOS(t, &stubSOSRepo{}, http.MethodPost, "/wbw/me/sos",
			map[string]any{"device_time": "x"}, true)},
		{"409", callSOS(t, &stubSOSRepo{cancelErr: repository.ErrSOSAlreadyAcked},
			http.MethodPost, "/wbw/me/sos/7/cancel", nil, true)},
	}
	for _, c := range cases {
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(c.rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body ไม่ใช่ JSON: %v", c.name, err)
		}
		if body.Error == "" {
			t.Fatalf("%s: body ต้องเป็น {\"error\":\"...\"} ที่ข้อความไม่ว่าง ได้ %s", c.name, c.rec.Body)
		}
	}
}
