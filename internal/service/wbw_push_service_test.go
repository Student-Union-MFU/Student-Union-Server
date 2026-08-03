package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"su-server/internal/model"
)

// FCM จริงยิงไม่ได้ในเทส (ต้องมี service account) แต่ส่วนที่พังเงียบที่สุดคือการ
// ตีความ error ของ FCM ผิด: จัดว่า token ตายทั้งที่แค่ล่มชั่วคราว = ลบ token ของ
// ผู้ใช้จริงทิ้ง · จัดว่ายังดีทั้งที่ตายแล้ว = ยิง push ใส่เครื่องที่ไม่มีอยู่ตลอดไป
// เทสชุดนี้ครอบ sendOne ด้วย FCM ปลอมที่ตอบตามที่กำหนด

func testPushService(t *testing.T, handler http.HandlerFunc) (*WBWPushService, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &WBWPushService{
		tokens:  oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}),
		sendURL: srv.URL,
		client:  srv.Client(),
	}, srv
}

func fcmError(status, errorCode string) string {
	return `{"error":{"status":"` + status + `","message":"boom","details":[` +
		`{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"` + errorCode + `"}]}}`
}

func TestSendOneClassifiesFCMErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantDead bool
		wantErr  bool
	}{
		{"สำเร็จ", 200, `{"name":"projects/p/messages/1"}`, false, false},
		{"ถอนแอปแล้ว", 404, fcmError("NOT_FOUND", "UNREGISTERED"), true, false},
		{"token เพี้ยน", 400, fcmError("INVALID_ARGUMENT", "INVALID_ARGUMENT"), true, false},
		{"404 ไม่มี details", 404, `{}`, true, false},
		// ต่อไปนี้คือความล้มเหลวชั่วคราว ห้ามลบ token ทิ้ง
		{"FCM ล่ม", 503, fcmError("UNAVAILABLE", "UNAVAILABLE"), false, true},
		{"โดน rate limit", 429, fcmError("RESOURCE_EXHAUSTED", "QUOTA_EXCEEDED"), false, true},
		{"credential หมดอายุ", 401, `{"error":{"status":"UNAUTHENTICATED"}}`, false, true},
		{"server พังภายใน", 500, `{"error":{"status":"INTERNAL"}}`, false, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := testPushService(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				io.WriteString(w, c.body)
			})
			dead, err := s.sendOne(context.Background(),
				&oauth2.Token{AccessToken: "x"}, fcmRequest{})

			if dead != c.wantDead {
				t.Errorf("dead = %v, ต้องการ %v", dead, c.wantDead)
			}
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, ต้องการ error = %v", err, c.wantErr)
			}
		})
	}
}

// payload ที่ iOS ต้องได้: type=chat ใช้แยกเส้นทางใน AppDelegate, collapse-id
// ต่อกลุ่มกันข้อความรัวๆ ท่วม lock screen, badge ต่อคน
func TestSendChatPayloadShape(t *testing.T) {
	var got fcmRequest
	var auth string
	s, _ := testPushService(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"name":"ok"}`)
	})

	badge := 7
	req := fcmRequest{Message: fcmMessage{
		Token:        "tok-1",
		Notification: &fcmNotification{Title: "Alice", Body: "สวัสดี"},
		Data:         map[string]string{"type": "chat", "group_id": "3", "message_id": "42"},
		APNS: &fcmAPNS{Headers: map[string]string{
			"apns-priority": "10", "apns-collapse-id": "chat-3",
		}},
	}}
	req.Message.APNS.Payload.Aps = fcmAps{Sound: "default", Badge: &badge, ThreadID: "chat-3"}

	if _, err := s.sendOne(context.Background(), &oauth2.Token{AccessToken: "test-token"}, req); err != nil {
		t.Fatalf("sendOne: %v", err)
	}

	if auth != "Bearer test-token" {
		t.Errorf("Authorization = %q", auth)
	}
	if got.Message.Data["type"] != "chat" {
		t.Errorf(`data.type = %q, ต้องเป็น "chat" ไม่งั้น iOS route ไปหน้าประกาศแทนแชท`, got.Message.Data["type"])
	}
	if got.Message.APNS.Headers["apns-collapse-id"] != "chat-3" {
		t.Errorf("collapse-id = %q", got.Message.APNS.Headers["apns-collapse-id"])
	}
	if got.Message.APNS.Payload.Aps.Badge == nil || *got.Message.APNS.Payload.Aps.Badge != 7 {
		t.Errorf("badge = %v, ต้องการ 7", got.Message.APNS.Payload.Aps.Badge)
	}
	if got.Message.APNS.Payload.Aps.ThreadID != "chat-3" {
		t.Errorf("thread-id = %q", got.Message.APNS.Payload.Aps.ThreadID)
	}
}

// badge 0 ต้องส่งไปจริง ไม่ใช่หายไปเพราะ omitempty — ศูนย์แปลว่า "ล้าง badge"
// ซึ่งเป็นค่าที่มีความหมาย ถ้าหายไป badge ค้างเลขเดิมบน icon
func TestBadgeZeroIsSent(t *testing.T) {
	var raw []byte
	s, _ := testPushService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"name":"ok"}`)
	})

	zero := 0
	req := fcmRequest{Message: fcmMessage{Token: "t", APNS: &fcmAPNS{}}}
	req.Message.APNS.Payload.Aps = fcmAps{Badge: &zero}

	if _, err := s.sendOne(context.Background(), &oauth2.Token{}, req); err != nil {
		t.Fatalf("sendOne: %v", err)
	}
	if !strings.Contains(string(raw), `"badge":0`) {
		t.Errorf("badge 0 หายไปจาก payload: %s", raw)
	}
}

// ไม่มี service account = ทุกอย่างต้องเป็น no-op เงียบๆ ไม่ panic
// (แชทในแอปต้องใช้ได้โดยไม่ต้องตั้ง Firebase ก่อน)
func TestPushDisabledWithoutCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("FIREBASE_SERVICE_ACCOUNT", "")

	s := NewWBWPushService(context.Background(), nil)
	if s.tokens != nil {
		t.Fatal("ไม่มี credential แต่ push ยังเปิดอยู่")
	}
	// repo เป็น nil — ถ้า SendChatPush ไม่ได้ออกตั้งแต่ต้น จะ panic ตรงนี้
	s.SendChatPush(context.Background(), model.Message{GroupID: 1, Body: "x"})
}

func TestSenderTitle(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name        string
		first, last *string
		want        string
	}{
		{"ชื่อเต็ม", str("Alice"), str("Smith"), "Alice Smith"},
		{"มีแต่ชื่อต้น", str("Alice"), nil, "Alice"},
		{"ไม่มีชื่อเลย (staff ที่ไม่มี profile)", nil, nil, "ข้อความใหม่"},
		{"ชื่อเป็นช่องว่าง", str("  "), str(""), "ข้อความใหม่"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := senderTitle(model.Message{FirstName: c.first, LastName: c.last})
			if got != c.want {
				t.Errorf("senderTitle = %q, ต้องการ %q", got, c.want)
			}
		})
	}
}
