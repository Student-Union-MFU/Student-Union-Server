package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"
)

/*
POST /wbw/me/feedback ครบทั้งห้าเคสตามตาราง Failure handling ของ spec
(docs/superpowers/specs/2026-08-02-checkin-feedback-design.md) — ฝั่งแอปตัดสินใจต่างกัน
สิ้นเชิงตาม status ที่ได้ (200/201 = สำเร็จ · 409/403 = สถานะปลายทาง ทิ้งของในคิว ·
400 = ทิ้ง · 5xx = เก็บไว้ retry) การ map error เป็น status จึงเป็นสัญญาที่ต้องตรึงไว้
ไม่ใช่รายละเอียดภายใน

เดินผ่าน middleware.RequireAuth ของจริงด้วย token ที่เซ็นจริง — เคส 401 จึงเป็นของจริง
ไม่ใช่การยัด claims เข้า context เอง
*/

const testParticipantID = "11111111-1111-1111-1111-111111111111"

// ตัวปลอมของ repository — service ของจริงยังทำงานเต็มตัว (ตรวจ rating/client_id, trim
// comment) มีแค่ชั้นที่คุยกับ Postgres เท่านั้นที่ถูกแทน
type fakeFeedbackRepo struct {
	saved   *model.CheckinFeedback
	created bool
	err     error

	gotParticipantID string
	gotRequest       model.FeedbackRequest
}

func (f *fakeFeedbackRepo) Submit(_ context.Context, participantID string, req model.FeedbackRequest) (*model.CheckinFeedback, bool, error) {
	f.gotParticipantID, f.gotRequest = participantID, req
	return f.saved, f.created, f.err
}

func (f *fakeFeedbackRepo) ListAll(context.Context) ([]model.AdminFeedbackRow, error) {
	return nil, nil
}

func (f *fakeFeedbackRepo) SummaryByCheckpoint(context.Context) ([]model.FeedbackSummary, error) {
	return nil, nil
}

// ยิงคำขอจริงผ่าน RequireAuth → handler · authorized=false = ไม่ใส่ header Authorization
func postFeedback(t *testing.T, repo *fakeFeedbackRepo, body string, authorized bool) *httptest.ResponseRecorder {
	t.Helper()

	tokens := service.NewWBWTokenService()
	h := NewWBWFeedbackHandler(service.NewWBWFeedbackService(repo))
	protected := middleware.RequireAuth(tokens)(http.HandlerFunc(h.Submit))

	req := httptest.NewRequest(http.MethodPost, "/wbw/me/feedback", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
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

func decodeFeedback(t *testing.T, rec *httptest.ResponseRecorder) model.CheckinFeedback {
	t.Helper()
	var out model.CheckinFeedback
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body ไม่ใช่ CheckinFeedback: %v (%s)", err, rec.Body.String())
	}
	return out
}

const validBody = `{"client_id":"c-1","checkpoint_id":7,"rating":3,"comment":"  ดีมาก  ","device_time":"2026-08-29T09:00:00Z"}`

// เคส 1 — บันทึกใหม่สำเร็จ = 201 · ตรวจด้วยว่า participant มาจาก claims ไม่ใช่จาก body
// (ถ้าหลุดให้ body กำหนดได้ คนหนึ่งจะตอบแทนคนอื่นได้) และ comment ถูก trim ก่อนถึง repo
func TestSubmitFeedbackCreatedReturns201(t *testing.T) {
	repo := &fakeFeedbackRepo{saved: &model.CheckinFeedback{ID: 1, CheckpointID: 7, Rating: 3}, created: true}
	rec := postFeedback(t, repo, validBody, true)

	if rec.Code != http.StatusCreated {
		t.Fatalf("ต้องได้ 201 ได้ %d (%s)", rec.Code, rec.Body.String())
	}
	if repo.gotParticipantID != testParticipantID {
		t.Fatalf("participant ต้องมาจาก token ได้ %q", repo.gotParticipantID)
	}
	if repo.gotRequest.Comment == nil || *repo.gotRequest.Comment != "ดีมาก" {
		t.Fatalf("comment ต้องถูก trim ก่อนลง repo ได้ %v", repo.gotRequest.Comment)
	}
	if got := decodeFeedback(t, rec); got.ID != 1 {
		t.Fatalf("ต้องคืนแถวที่บันทึก ได้ %+v", got)
	}
}

// เคส 2 — client_id เดิม (outbox retry ตอนเน็ตกลับมา) = 200 ไม่ใช่ 409
// แอปถือว่าทั้งคู่คือ .saved แต่ 409 มีความหมายอื่น (ไปล้างของในคิวทั้งฐาน) การแยกจึงสำคัญ
func TestSubmitFeedbackRetrySameClientIDReturns200(t *testing.T) {
	repo := &fakeFeedbackRepo{saved: &model.CheckinFeedback{ID: 1, CheckpointID: 7, Rating: 3}, created: false}
	rec := postFeedback(t, repo, validBody, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("ส่งซ้ำด้วย client_id เดิมต้องได้ 200 ได้ %d", rec.Code)
	}
}

// เคส 3 — rating นอกช่วง 1..3 = 400 · ต้องไม่ไปแตะ repository เลย
func TestSubmitFeedbackBadRatingReturns400(t *testing.T) {
	repo := &fakeFeedbackRepo{}
	rec := postFeedback(t, repo,
		`{"client_id":"c-1","checkpoint_id":7,"rating":9,"device_time":"2026-08-29T09:00:00Z"}`, true)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rating ผิดต้องได้ 400 ได้ %d", rec.Code)
	}
	if repo.gotParticipantID != "" {
		t.Fatal("rating ผิดต้องถูกปัดตกก่อนถึงฐานข้อมูล")
	}
}

// client_id ว่าง = 400 เหมือนกัน — ไม่มี client_id แปลว่า idempotency พังทั้งเส้น
// ปล่อยผ่านจะได้แถวซ้ำทุกครั้งที่ outbox retry
func TestSubmitFeedbackMissingClientIDReturns400(t *testing.T) {
	repo := &fakeFeedbackRepo{}
	rec := postFeedback(t, repo,
		`{"client_id":"   ","checkpoint_id":7,"rating":3,"device_time":"2026-08-29T09:00:00Z"}`, true)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("client_id ว่างต้องได้ 400 ได้ %d", rec.Code)
	}
	if repo.gotParticipantID != "" {
		t.Fatal("client_id ว่างต้องถูกปัดตกก่อนถึงฐานข้อมูล")
	}
}

// เคส 4 — ยังไม่เช็คอินฐานนี้ (รวมฐานบริการที่ requires_checkin = false) = 403
func TestSubmitFeedbackNotCheckedInReturns403(t *testing.T) {
	repo := &fakeFeedbackRepo{err: repository.ErrNotCheckedIn}
	rec := postFeedback(t, repo, validBody, true)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("ยังไม่เช็คอินต้องได้ 403 ได้ %d", rec.Code)
	}
}

// เคส 5 — ตอบฐานนี้ไปแล้วด้วย client_id อื่น = 409 พร้อม "คำตอบเดิม" ใน body
// ฟอร์มเอาคำตอบเดิมไปแสดงแบบอ่านอย่างเดียว 409 ที่ body ว่างจะทำให้ฟอร์มโชว์ค่าว่าง
func TestSubmitFeedbackAlreadyAnsweredReturns409WithExistingRow(t *testing.T) {
	existing := &model.CheckinFeedback{ID: 42, CheckpointID: 7, Rating: 2}
	repo := &fakeFeedbackRepo{err: repository.ErrAlreadyAnswered{Existing: existing}}
	rec := postFeedback(t, repo, validBody, true)

	if rec.Code != http.StatusConflict {
		t.Fatalf("ตอบไปแล้วต้องได้ 409 ได้ %d", rec.Code)
	}
	got := decodeFeedback(t, rec)
	if got.ID != 42 || got.Rating != 2 {
		t.Fatalf("409 ต้องพาคำตอบเดิมกลับไปให้ฟอร์มแสดง ได้ %+v", got)
	}
}

/*
สองเทสถัดไปตรึง **รูปร่างของ body** ไม่ใช่แค่ status — เพราะแอปใช้ body เป็นตัวตัดสินใจไปแล้ว

Cloudflare หน้า api.studentunion.social ตอบ 403 ได้เองจาก WAF/firewall rule โดยที่ request
ไม่เคยถึง origin เลย ถ้าแอปอ่านแค่ status มันจะแปล 403 นั้นเป็น "ยังไม่ได้เช็คอินฐานนี้" แล้ว
**ลบคิวความเห็นของฐานนั้นทิ้งทั้งฐาน** ทั้งที่ผู้ใช้เพิ่งเห็นข้อความ "ส่งความเห็นแล้ว ขอบคุณ"

แอปจึงถือ 403/409 เป็นสถานะปลายทางเฉพาะเมื่อ body เป็นของ origin จริง (ดู
APIClient.submitFeedback ฝั่ง iOS) — คีย์ที่มันมองหาคือสัญญาที่ตรึงไว้ที่นี่ ถ้าใครเปลี่ยนรูป
body ตรงนี้โดยไม่แก้ฝั่งแอปด้วย ผลไม่ใช่ข้อความเพี้ยน แต่คือคิวค้างถาวรที่ไม่มีวันส่งได้
*/

// 403 ต้องเป็น error envelope {"error":"..."} ที่ข้อความไม่ว่าง
func TestSubmitFeedbackForbiddenBodyIsErrorEnvelope(t *testing.T) {
	repo := &fakeFeedbackRepo{err: repository.ErrNotCheckedIn}
	rec := postFeedback(t, repo, validBody, true)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("403 ต้องมี body เป็น JSON object: %v (%s)", err, rec.Body.String())
	}
	msg, ok := body["error"].(string)
	if !ok || msg == "" {
		t.Fatalf("403 ต้องมีคีย์ error ที่เป็นสตริงไม่ว่าง — แอปใช้คีย์นี้แยก 403 ของเราออกจาก "+
			"403 ของ Cloudflare ได้ %s", rec.Body.String())
	}
}

// 409 ต้องเป็น "แถวความเห็นเดิม" (มี checkpoint_id) ไม่ใช่ envelope และไม่ใช่ body ว่าง
func TestSubmitFeedbackConflictBodyIsFeedbackRow(t *testing.T) {
	existing := &model.CheckinFeedback{ID: 42, CheckpointID: 7, Rating: 2}
	repo := &fakeFeedbackRepo{err: repository.ErrAlreadyAnswered{Existing: existing}}
	rec := postFeedback(t, repo, validBody, true)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("409 ต้องมี body เป็น JSON object: %v (%s)", err, rec.Body.String())
	}
	if _, ok := body["checkpoint_id"]; !ok {
		t.Fatalf("409 ต้องมีคีย์ checkpoint_id — แอปใช้คีย์นี้ยืนยันว่า 409 มาจาก origin จริง "+
			"ไม่ใช่จาก edge ได้ %s", rec.Body.String())
	}
}

// error อื่นที่ไม่รู้จัก = 500 · ฝั่งแอปหลังแก้รอบนี้ถือว่า 5xx retry ได้และเก็บ draft ไว้
// ในคิว (ดู FeedbackStore ฝั่ง iOS) ถ้าหลุดไปตอบ 4xx ตรงนี้ คำตอบของผู้ใช้จะถูกทิ้ง
func TestSubmitFeedbackUnknownErrorReturns500(t *testing.T) {
	repo := &fakeFeedbackRepo{err: errors.New("db ล่ม")}
	rec := postFeedback(t, repo, validBody, true)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("error ที่ไม่รู้จักต้องได้ 500 ได้ %d", rec.Code)
	}
}

// ไม่มี token = 401 และต้องไม่ถึง repository
func TestSubmitFeedbackWithoutTokenReturns401(t *testing.T) {
	repo := &fakeFeedbackRepo{}
	rec := postFeedback(t, repo, validBody, false)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ไม่มี token ต้องได้ 401 ได้ %d", rec.Code)
	}
	if repo.gotParticipantID != "" {
		t.Fatal("คำขอที่ไม่ผ่าน auth ต้องไม่ถึงฐานข้อมูล")
	}
}

// body ที่ไม่ใช่ JSON = 400 (ไม่ใช่ 500) — panic ตรงนี้เคยเป็นทางล้มเซิร์ฟเวอร์แบบง่ายๆ
func TestSubmitFeedbackMalformedJSONReturns400(t *testing.T) {
	repo := &fakeFeedbackRepo{}
	rec := postFeedback(t, repo, `{"client_id":`, true)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("JSON พังต้องได้ 400 ได้ %d", rec.Code)
	}
}
