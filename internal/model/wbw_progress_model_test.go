package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// เหตุผลเดียวกับ booth_model_test.go: SUS ไม่มี test harness ที่ต่อ DB จริง (ดู Global
// Constraints ของแผนงาน 2026-08-02-forest-3d-background) — ชั้น marshal นี้จึงเป็นจุดเดียว
// ที่พิสูจน์ contract ของ JSON ได้อัตโนมัติ ไม่ต้องพึ่ง curl มือกับ stack ที่รันอยู่ ก่อนหน้านี้
// endpoint /wbw/me/progress ถูกยืนยันด้วย curl เท่านั้น (Task 2 ของแผนงาน) เทสนี้ปักหมุด
// สัญญาที่ฝั่ง iOS พึ่งพาไว้แทน

// Go marshal nil slice เป็น null — iOS decode `[CheckinProgressItem]` (ไม่ใช่ optional) เจอ
// null แล้วพัง (บั๊กคลาสเดียวกับที่ booth_model_test.go กันไว้ให้ /events) repository.Progress()
// ต้อง init CheckedIn เป็น []CheckinProgressItem{} เสมอ ไม่ใช่ปล่อยเป็น nil ตามค่าเริ่มต้น
func TestCheckinProgressEmptyCheckedInMarshalsAsArray(t *testing.T) {
	p := CheckinProgress{Total: 8, CheckedIn: []CheckinProgressItem{}}

	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if !strings.Contains(string(encoded), `"checked_in":[]`) {
		t.Errorf("expected \"checked_in\":[], got %s", encoded)
	}
}

// Sequence เป็น *int เพราะบางฐาน (ที่ไม่ผูก sequence ในตาราง checkpoint) ไม่มีลำดับ — ต้องได้
// null จริงๆ ไม่ใช่ 0 (0 จะชนกับฐานที่ sequence เป็น 0 จริงถ้ามีวันหนึ่ง)
func TestCheckinProgressItemNilSequenceMarshalsAsNull(t *testing.T) {
	item := CheckinProgressItem{CheckpointID: 5, Name: "จุดปลูก", Sequence: nil, At: "2026-08-29T10:00:00Z"}

	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if !strings.Contains(string(encoded), `"sequence":null`) {
		t.Errorf("expected \"sequence\":null, got %s", encoded)
	}
}

// ฝั่ง iOS (CheckinProgress ใน WBW/Models.swift) decode key พวกนี้ตรงๆ ด้วยชื่อ snake_case —
// พิมพ์ struct tag ผิดที่นี่จะไม่มี compiler ฝั่งไหนจับให้ เทสนี้ตรึงชื่อ key ไว้
func TestCheckinProgressKeepsTheFieldsTheClientNeeds(t *testing.T) {
	seq := 1
	p := CheckinProgress{
		Total: 8,
		CheckedIn: []CheckinProgressItem{
			{CheckpointID: 1, Name: "ฐาน 1", Sequence: &seq, At: "2026-08-29T09:00:00Z"},
		},
	}

	var decoded map[string]any
	encoded, _ := json.Marshal(p)
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	for _, key := range []string{"total", "checked_in"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing %q in %v", key, decoded)
		}
	}
}

// เหมือนกันกับข้างบนแต่ระดับ item — checkpoint_id/name/sequence/at ทั้ง 4 ต้องอยู่ครบ
func TestCheckinProgressItemKeepsTheFieldsTheClientNeeds(t *testing.T) {
	seq := 3
	item := CheckinProgressItem{CheckpointID: 1, Name: "ลานวัฒนธรรม", Sequence: &seq, At: "2026-08-29T09:00:00Z"}

	var decoded map[string]any
	encoded, _ := json.Marshal(item)
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	for _, key := range []string{"checkpoint_id", "name", "sequence", "at"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing %q in %v", key, decoded)
		}
	}
}

// ฐานที่ยังไม่ตอบต้องส่ง answered=false พร้อม rating/comment เป็น null ไม่ใช่คีย์หาย —
// แอปแยก "ยังไม่ตอบ" กับ "ตอบแล้ว" จากคีย์นี้
func TestProgressItemCarriesFeedbackKeysWhenUnanswered(t *testing.T) {
	out, err := json.Marshal(CheckinProgressItem{CheckpointID: 1, Name: "ฐาน", At: "t"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"answered":false`, `"rating":null`, `"comment":null`, `"activity_name":null`} {
		if !strings.Contains(string(out), key) {
			t.Fatalf("expected %s in %s", key, out)
		}
	}
}

func TestProgressItemCarriesAnswerWhenAnswered(t *testing.T) {
	rating := 3
	comment := "ดีมาก"
	activity := "ปลูกป่า"
	out, _ := json.Marshal(CheckinProgressItem{
		CheckpointID: 5, Name: "จุดปลูก", ActivityName: &activity, At: "t",
		Answered: true, Rating: &rating, Comment: &comment,
	})
	for _, key := range []string{`"answered":true`, `"rating":3`, `"comment":"ดีมาก"`, `"activity_name":"ปลูกป่า"`} {
		if !strings.Contains(string(out), key) {
			t.Fatalf("expected %s in %s", key, out)
		}
	}
}

// emergency_phone ต้องอยู่ใน JSON เสมอ (ดูคอมเมนต์ EmergencyPhone บน CheckinProgress) — /me/progress
// ถูก poll ทุก 60 วิระหว่างเปิดแอป ปุ่มโทรสำรองฝั่ง iOS จึงมีเบอร์แคชไว้ก่อนเกิดเหตุ พิมพ์ struct tag
// ผิดตรงนี้จะทำให้แอปเห็นคีย์หายไปเฉยๆ ไม่มี compiler ฝั่งไหนจับให้
func TestCheckinProgressCarriesTheEmergencyPhoneKey(t *testing.T) {
	p := CheckinProgress{Total: 8, CheckedIn: []CheckinProgressItem{}, EmergencyPhone: "053-916-000"}

	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(encoded), `"emergency_phone":"053-916-000"`) {
		t.Errorf("expected \"emergency_phone\":\"053-916-000\", got %s", encoded)
	}
}

// ว่าง = dev ไม่ได้ตั้งเบอร์กลาง เป็นค่าที่ใช้ได้ปกติ — คีย์ต้องยังอยู่พร้อมค่าว่าง ไม่ใช่คีย์หายไป
func TestCheckinProgressEmergencyPhoneCanMarshalEmpty(t *testing.T) {
	p := CheckinProgress{Total: 8, CheckedIn: []CheckinProgressItem{}}

	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(encoded), `"emergency_phone":""`) {
		t.Errorf("expected \"emergency_phone\":\"\", got %s", encoded)
	}
}
