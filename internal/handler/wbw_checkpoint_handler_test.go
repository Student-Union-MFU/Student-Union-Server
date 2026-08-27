package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/service"
)

/*
GET /wbw/checkpoints — รายการฐานทั้งงานสำหรับผู้เข้าร่วม

สองเรื่องที่ต้องตรึงไว้ ไม่ใช่รายละเอียดภายใน:

 1. **401 เมื่อไม่ได้ล็อกอิน** ตารางฐานไม่ใช่ความลับ แต่ endpoint นี้ถูกวางไว้หลัง requireAuth
    โดยตั้งใจ (คนที่เรียกคือแท็บแผนที่ซึ่งอยู่หลังล็อกอินอยู่แล้ว) เผลอย้ายบรรทัดใน cmd/main.go
    ขึ้นไปเหนือกลุ่ม requireAuth เมื่อไหร่ มันจะกลายเป็น public โดยไม่มีอะไรฟ้อง
 2. **ลิสต์ว่างต้องเป็น `[]` ไม่ใช่ `null`** — ฝั่ง iOS ถอดรหัสเป็น array ตรง ๆ เจอ null แล้ว
    decode พังทั้งก้อน ซึ่งบนจอคือชื่อฐานหายหมดทั้งแผนที่ ไม่ใช่ error ที่อ่านออก

เดินผ่าน middleware.RequireAuth ของจริงด้วย token ที่เซ็นจริง เคส 401 จึงเป็นของจริง
ไม่ใช่การยัด claims เข้า context เอง (แพทเทิร์นเดียวกับ wbw_feedback_handler_test.go)
*/

type fakeCheckpointRepo struct {
	list []model.ParticipantCheckpoint
	err  error
}

func (f *fakeCheckpointRepo) ListForParticipant(context.Context) ([]model.ParticipantCheckpoint, error) {
	return f.list, f.err
}

func getCheckpoints(t *testing.T, repo *fakeCheckpointRepo, authorized bool) *httptest.ResponseRecorder {
	t.Helper()

	tokens := service.NewWBWTokenService()
	h := NewWBWCheckpointHandler(service.NewWBWCheckpointService(repo))
	protected := middleware.RequireAuth(tokens)(http.HandlerFunc(h.List))

	req := httptest.NewRequest(http.MethodGet, "/wbw/checkpoints", nil)
	if authorized {
		tok, err := tokens.Sign(testParticipantID, "participant", "6931900011")
		if err != nil {
			t.Fatalf("เซ็น token ไม่สำเร็จ: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	return rec
}

func seq(n int) *int       { return &n }
func str(s string) *string { return &s }

func TestCheckpointsRequireLogin(t *testing.T) {
	rec := getCheckpoints(t, &fakeCheckpointRepo{}, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ไม่มี token ต้องได้ 401 ไม่ใช่ %d — endpoint หลุดเป็น public แล้ว (%s)",
			rec.Code, rec.Body.String())
	}
}

func TestCheckpointsReturnEveryRowWithBothLanguages(t *testing.T) {
	repo := &fakeCheckpointRepo{list: []model.ParticipantCheckpoint{
		{ID: 1, Sequence: seq(1), Name: "วิหารพระเจ้าล้านทอง", NameEn: str("Wihan Phra Chao Lan Thong"),
			ActivityName: str("ไหว้พระ"), ActivityNameEn: str("Pay respects"),
			Type: "activity", RequiresCheckin: true},
		{ID: 9, Sequence: nil, Name: "MFU Botanical Garden", NameEn: str("MFU Botanical Garden"),
			ActivityName: str("จุดห้องน้ำ"), ActivityNameEn: str("Restroom point"),
			Type: "restroom", RequiresCheckin: false},
	}}
	rec := getCheckpoints(t, repo, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("ต้องได้ 200 ไม่ใช่ %d (%s)", rec.Code, rec.Body.String())
	}

	var out []model.ParticipantCheckpoint
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body ไม่ใช่ array ของ ParticipantCheckpoint: %v (%s)", err, rec.Body.String())
	}
	if len(out) != 2 {
		t.Fatalf("ต้องได้ครบทุกแถวที่ repo คืนมา ได้ %d", len(out))
	}
	// จุดบริการไม่มี sequence — ต้องเป็น null ไม่ใช่ 0 ไม่งั้นแอปจะอ่านว่าเป็น "ฐานที่ 0"
	if out[1].Sequence != nil {
		t.Fatalf("จุดบริการต้องมี sequence เป็น null ได้ %v", *out[1].Sequence)
	}
	if out[0].NameEn == nil || *out[0].NameEn == "" {
		t.Fatalf("ชื่ออังกฤษหาย — คนที่ตั้งแอปเป็นอังกฤษจะเห็นชื่อฐานเป็นไทยทั้งหมด")
	}
	if out[0].ActivityNameEn == nil || *out[0].ActivityNameEn == "" {
		t.Fatalf("ชื่อกิจกรรมภาษาอังกฤษหาย")
	}
}

func TestCheckpointsEmptyListIsArrayNotNull(t *testing.T) {
	rec := getCheckpoints(t, &fakeCheckpointRepo{list: []model.ParticipantCheckpoint{}}, true)
	if body := rec.Body.String(); body == "null" || body == "null\n" {
		t.Fatalf("ลิสต์ว่างต้องเป็น [] ไม่ใช่ null — ตัวถอดรหัสฝั่ง iOS พังทั้งก้อน ชื่อฐานหายหมดทั้งแผนที่")
	}
}

func TestCheckpointsRepoFailureIs500(t *testing.T) {
	rec := getCheckpoints(t, &fakeCheckpointRepo{err: errors.New("boom")}, true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("repo พังต้องได้ 500 ไม่ใช่ %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("error ต้องเป็น {\"error\":\"...\"} ที่ frontend อ่านได้ ไม่ใช่ข้อความเปล่า (%s)",
			rec.Body.String())
	}
}
