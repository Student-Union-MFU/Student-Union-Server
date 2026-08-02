package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// สัญญา JSON ที่ฝั่ง iOS decode — เทสไว้เพราะ SUS ไม่มี harness เทส DB
// รูป JSON เป็นสิ่งเดียวที่เทสได้โดยไม่ต้องต่อฐานข้อมูล และเป็นสิ่งที่พังเงียบที่สุด
func TestFeedbackRequestDecodesSnakeCase(t *testing.T) {
	raw := `{"client_id":"6f9d1e7a-0000-4000-8000-000000000001","checkpoint_id":3,
	         "rating":2,"comment":"สนุกดี","device_time":"2026-08-29T09:12:03Z"}`
	var req FeedbackRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.CheckpointID != 3 || req.Rating != 2 {
		t.Fatalf("got checkpoint=%d rating=%d", req.CheckpointID, req.Rating)
	}
	if req.Comment == nil || *req.Comment != "สนุกดี" {
		t.Fatalf("comment = %v", req.Comment)
	}
}

func TestFeedbackRequestAllowsMissingComment(t *testing.T) {
	var req FeedbackRequest
	if err := json.Unmarshal([]byte(`{"client_id":"x","checkpoint_id":1,"rating":3,"device_time":"t"}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Comment != nil {
		t.Fatalf("comment should be nil, got %v", *req.Comment)
	}
}

// ref_id ต้องอยู่ใน JSON เสมอ แม้เป็น null — แอปอ่านคีย์นี้เพื่อรู้ว่าแจ้งเตือนชี้ไปฐานไหน
// ถ้าใส่ omitempty คีย์จะหายไปทั้งดวงตอนเป็น nil และ decoder ฝั่ง iOS จะเจอ key ไม่ครบ
func TestNotificationAlwaysCarriesRefIDKey(t *testing.T) {
	out, err := json.Marshal(Notification{ID: 1, Type: "announcement", Title: "t"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"ref_id":null`) {
		t.Fatalf(`expected "ref_id":null in %s`, out)
	}

	ref := "7"
	out, _ = json.Marshal(Notification{ID: 2, Type: "checkin_feedback", Title: "t", RefID: &ref})
	if !strings.Contains(string(out), `"ref_id":"7"`) {
		t.Fatalf(`expected "ref_id":"7" in %s`, out)
	}
}
