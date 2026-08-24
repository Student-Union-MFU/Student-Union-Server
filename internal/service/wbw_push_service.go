package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"su-server/internal/model"
	"su-server/internal/repository"
)

// ยิง FCM HTTP v1 ตรงด้วย oauth2 + net/http แทน firebase-admin SDK
//
// SDK ลาก firestore/storage/monitoring/grpc/xds มาทั้งกอง (ไบนารี 15MB -> 52MB,
// module 17 -> 62) เพื่อฟังก์ชันที่ใช้จริงไม่กี่บรรทัด · v1 ไม่มี batch endpoint
// แล้วตั้งแต่ปี 2024 SDK เองก็ยิงทีละ request พร้อมกัน ซึ่งทำเองได้ตรงๆ
const (
	fcmScope      = "https://www.googleapis.com/auth/firebase.messaging"
	fcmSendURL    = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
	pushBodyLimit = 120
	// ยิงพร้อมกันได้กี่เครื่อง — กันเปิด connection ทีเดียวเป็นพัน
	pushConcurrency = 16
	// เพดานเวลาส่ง push หนึ่งข้อความ (ทุกเครื่องรวมกัน) ไม่ผูกกับ request ที่จบไปแล้ว
	pushTimeout = 20 * time.Second
)

// WBWPushService ส่ง push ผ่าน FCM (ครอบ APNs ให้ iOS)
//
// ไม่มี service account = tokens เป็น nil และทุกการเรียกกลายเป็น no-op
// ตั้งใจให้เป็นแบบนั้น: แชทในแอปทำงานครบโดยไม่ต้องตั้ง Firebase ก่อน
type WBWPushService struct {
	repo    *repository.WBWDeviceRepository
	tokens  oauth2.TokenSource
	sendURL string
	client  *http.Client

	// ตัวนับสำหรับหน้า stats — atomic เพราะ sendOne วิ่งพร้อมกันได้ถึง pushConcurrency
	// ตัว และเลขพวกนี้ต้องไม่ไปแย่ง lock กับงานส่งจริง
	//
	// ทำไมต้องนับ: push เป็นงาน fire-and-forget ทั้งหมด ล้มแล้วไม่มี request ไหนพัง
	// error ไปจบที่ slog.Warn บรรทัดเดียวแล้วหายไปกับ log · ถ้า FCM ปฏิเสธทุกใบ
	// (service account หมดอายุ/โปรเจกต์ถูกปิด) อาการที่ผู้ใช้เจอคือ "ไม่มีแจ้งเตือน"
	// ซึ่งไม่มีใครแจ้ง และไม่มีอะไรในโปรเซสบอกได้เลยว่าเกิดขึ้นแล้ว
	sendsAttempted atomic.Int64
	sendsFailed    atomic.Int64
	tokensReaped   atomic.Int64
}

// NewWBWPushService อ่าน credential จาก GOOGLE_APPLICATION_CREDENTIALS
// (หรือ FIREBASE_SERVICE_ACCOUNT ตามชื่อที่ฝั่ง Node ใช้)
func NewWBWPushService(ctx context.Context, repo *repository.WBWDeviceRepository) *WBWPushService {
	s := &WBWPushService{repo: repo}

	path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if path == "" {
		path = os.Getenv("FIREBASE_SERVICE_ACCOUNT")
	}
	if path == "" {
		slog.Warn("push ปิดอยู่: ไม่มี GOOGLE_APPLICATION_CREDENTIALS — แชทในแอปยังทำงานปกติ")
		return s
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		slog.Error("push init: อ่าน service account ไม่ได้ — ปิด push ไว้", "err", err)
		return s
	}
	// CredentialsFromJSON ให้ทั้ง TokenSource (รีเฟรช access token เองอัตโนมัติ)
	// และ project id ที่ต้องเอาไปประกอบ URL
	creds, err := google.CredentialsFromJSON(ctx, raw, fcmScope)
	if err != nil {
		slog.Error("push init: service account ใช้ไม่ได้ — ปิด push ไว้", "err", err)
		return s
	}
	if creds.ProjectID == "" {
		slog.Error("push init: service account ไม่มี project_id — ปิด push ไว้")
		return s
	}

	s.tokens = creds.TokenSource
	s.sendURL = fmt.Sprintf(fcmSendURL, creds.ProjectID)
	s.client = &http.Client{Timeout: 10 * time.Second}
	slog.Info("push พร้อมใช้งาน (FCM HTTP v1)", "project", creds.ProjectID)
	return s
}

/* ---------- โครงสร้าง payload ของ FCM HTTP v1 ---------- */

type fcmAps struct {
	Sound    string `json:"sound,omitempty"`
	Badge    *int   `json:"badge,omitempty"`
	ThreadID string `json:"thread-id,omitempty"`
	// InterruptionLevel — เฉพาะ sendTokens/SOS ใช้ (ดูคอมเมนต์ที่ SendToTokens) ตัวอื่น
	// ไม่ตั้งค่านี้ omitempty จึงทำให้หายไปจาก payload เหมือนเดิมสำหรับแชท/push รายคน
	InterruptionLevel string `json:"interruption-level,omitempty"`
}

type fcmAPNS struct {
	Headers map[string]string `json:"headers,omitempty"`
	Payload struct {
		Aps fcmAps `json:"aps"`
	} `json:"payload"`
}

type fcmNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification *fcmNotification  `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	APNS         *fcmAPNS          `json:"apns,omitempty"`
}

type fcmRequest struct {
	Message fcmMessage `json:"message"`
}

// รูปแบบ error ของ FCM v1 — errorCode ที่ต้องสนใจอยู่ใน details
//
//	{"error":{"status":"NOT_FOUND","details":[
//	  {"@type":"...FcmError","errorCode":"UNREGISTERED"}]}}
type fcmErrorResponse struct {
	Error struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// SendChatPush — fire-and-forget · คืนทันที ให้ handler ตอบ 201 ได้โดยไม่ต้องรอ FCM
//
// ใช้ context.WithoutCancel เพราะ context ของ request จะถูกยกเลิกทันทีที่ตอบ response
// เสร็จ ถ้าเอา ctx เดิมไปใช้ใน goroutine push จะโดนยกเลิกแทบทุกครั้ง
//
// ผ่าน goSafe ไม่ใช่ go ตรงๆ — panic ใน goroutine ที่แตกเองไม่มี chi.Recoverer ครอบให้
// จะฆ่าโปรเซสทั้งตัว (ดูคอมเมนต์ที่ goSafe) push เป็นงานเสริม ล้มทั้งเซิร์ฟเวอร์ไม่ได้
func (s *WBWPushService) SendChatPush(ctx context.Context, msg model.Message) {
	if s.tokens == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	goSafe("SendChatPush", func() {
		c, cancel := context.WithTimeout(detached, pushTimeout)
		defer cancel()
		if err := s.sendChat(c, msg); err != nil {
			slog.Error("ส่ง push แชทไม่สำเร็จ", "group_id", msg.GroupID, "err", err)
		}
	})
}

func (s *WBWPushService) sendChat(ctx context.Context, msg model.Message) error {
	targets, err := s.repo.ChatPushTargets(ctx, msg.GroupID, msg.SenderID)
	if err != nil || len(targets) == 0 {
		return err
	}

	// ขอ access token ครั้งเดียวใช้ทั้งรอบ · TokenSource cache ให้อยู่แล้ว
	// ไม่ต้องขอใหม่ทุกเครื่อง
	tok, err := s.tokens.Token()
	if err != nil {
		return fmt.Errorf("ขอ access token ไม่ได้: %w", err)
	}

	title := senderTitle(msg)
	body := []rune(msg.Body)
	if len(body) > pushBodyLimit {
		body = body[:pushBodyLimit]
	}
	groupID := strconv.Itoa(msg.GroupID)
	thread := "chat-" + groupID
	data := map[string]string{
		// iOS อ่าน type=="chat" เพื่อแยกเส้นทาง (ดู AppDelegate ฝั่งแอป)
		"type":       "chat",
		"group_id":   groupID,
		"message_id": strconv.FormatInt(msg.ID, 10),
	}

	var (
		mu      sync.Mutex
		invalid []string
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, pushConcurrency)

	// goSafe ไม่ใช่ go ตรงๆ เหมือนกัน — panic ในนี้ก็ฆ่าโปรเซสได้เท่ากับตัวข้างนอก
	// (recover ของ SendChatPush อยู่คนละ goroutine กัน ครอบตัวลูกไม่ได้) · wg.Done กับ
	// การคืน sem เป็น defer ที่อยู่ "ใน" f จึงยังทำงานครบก่อน panic จะไหลไปถึง recover
	// wg.Wait() ข้างล่างไม่ค้าง · ตัวแปร t ปลอดภัยที่จะ capture ตรงๆ (Go 1.22+ ให้ตัวใหม่ทุกรอบ)
	for _, t := range targets {
		wg.Add(1)
		goSafe("sendChat.target", func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			badge := t.Badge
			req := fcmRequest{Message: fcmMessage{
				Token:        t.Token,
				Notification: &fcmNotification{Title: title, Body: string(body)},
				Data:         data,
				APNS: &fcmAPNS{Headers: map[string]string{
					"apns-priority": "10",
					// ข้อความรัวๆ ทับอันเดิมแทนที่จะท่วม lock screen
					"apns-collapse-id": thread,
				}},
			}}
			req.Message.APNS.Payload.Aps = fcmAps{Sound: "default", Badge: &badge, ThreadID: thread}

			dead, err := s.sendOne(ctx, tok, req)
			if err != nil {
				slog.Warn("push หนึ่งเครื่องล้มเหลว", "err", err)
				return
			}
			if dead {
				mu.Lock()
				invalid = append(invalid, t.Token)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(invalid) > 0 {
		slog.Info("เก็บกวาด device token ที่ใช้ไม่ได้", "count", len(invalid))
		s.tokensReaped.Add(int64(len(invalid)))
		return s.repo.DeleteTokens(ctx, invalid)
	}
	return nil
}

// SendUserPush — push เข้าเครื่องของผู้ใช้คนเดียว · fire-and-forget
//
// ต้องไม่ทำให้คนเรียกช้าหรือพัง: เจ้าหน้าที่ยืนอยู่หน้าคิวตอนสแกน ถ้า FCM ช้า
// หรือล่มแล้วลากให้ /staff/checkin ตอบช้า คิวก็ยาวขึ้นทันที
// context.WithoutCancel เพราะ ctx ของ request ถูกยกเลิกทันทีที่ตอบ response เสร็จ
// และผ่าน goSafe ด้วยเหตุผลเดียวกับ SendChatPush (ดูคอมเมนต์ที่ goSafe)
func (s *WBWPushService) SendUserPush(ctx context.Context, userID, title, body string, data map[string]string) {
	if s.tokens == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	goSafe("SendUserPush", func() {
		c, cancel := context.WithTimeout(detached, pushTimeout)
		defer cancel()
		if err := s.sendUser(c, userID, title, body, data); err != nil {
			slog.Error("ส่ง push รายคนไม่สำเร็จ", "user_id", userID, "err", err)
		}
	})
}

// sendUser — เหมือน sendChat ทุกขั้นตอน (ขอ token ครั้งเดียว ยิงแต่ละเครื่องพร้อมกัน
// เก็บกวาด token ตายผ่าน sendOne ตัวเดียวกัน) ต่างแค่เป้าหมายเป็นทุกเครื่องของคนเดียว
// ไม่ใช่ทั้งกลุ่ม · title/body/data มาจากผู้เรียกแทนที่จะคำนวณจาก model.Message
// badge ใช้ t.Badge ตรงๆ ซึ่ง UserPushTargets ตั้งเป็น 0 เสมอ (ดู comment ที่นั่น)
func (s *WBWPushService) sendUser(ctx context.Context, userID, title, body string, data map[string]string) error {
	targets, err := s.repo.UserPushTargets(ctx, userID)
	if err != nil || len(targets) == 0 {
		return err
	}

	// ขอ access token ครั้งเดียวใช้ทั้งรอบ · TokenSource cache ให้อยู่แล้ว
	// ไม่ต้องขอใหม่ทุกเครื่อง
	tok, err := s.tokens.Token()
	if err != nil {
		return fmt.Errorf("ขอ access token ไม่ได้: %w", err)
	}

	var (
		mu      sync.Mutex
		invalid []string
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, pushConcurrency)

	// goSafe ด้วยเหตุผลเดียวกับใน sendChat (ดูคอมเมนต์ที่นั่น)
	for _, t := range targets {
		wg.Add(1)
		goSafe("sendUser.target", func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			badge := t.Badge
			req := fcmRequest{Message: fcmMessage{
				Token:        t.Token,
				Notification: &fcmNotification{Title: title, Body: body},
				Data:         data,
				APNS:         &fcmAPNS{Headers: map[string]string{"apns-priority": "10"}},
			}}
			req.Message.APNS.Payload.Aps = fcmAps{Sound: "default", Badge: &badge}

			dead, err := s.sendOne(ctx, tok, req)
			if err != nil {
				slog.Warn("push หนึ่งเครื่องล้มเหลว", "err", err)
				return
			}
			if dead {
				mu.Lock()
				invalid = append(invalid, t.Token)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(invalid) > 0 {
		slog.Info("เก็บกวาด device token ที่ใช้ไม่ได้", "count", len(invalid))
		s.tokensReaped.Add(int64(len(invalid)))
		return s.repo.DeleteTokens(ctx, invalid)
	}
	return nil
}

// SendToTokens — ยิง push ไปยัง token ที่ผู้เรียกหามาเองแล้ว
//
// ต่างจาก SendChatPush/SendUserPush ตรงที่ปลายทางไม่ได้มาจาก query ตายตัวหนึ่งอัน
// — SOS มีสามกลุ่มผู้รับที่คิดคนละแบบ (ฐาน/ทีมกลาง/กลุ่มเพื่อน) การรวมเข้ามาเป็น
// query เดียวจะทำให้ทั้งสามกลุ่มถูกบังคับให้ได้ข้อความเดียวกัน ซึ่งผิดตั้งแต่ต้น
//
// interruption-level: time-sensitive ทะลุ Focus ได้โดยไม่ต้องขอ entitlement
// critical alert จาก Apple (ซึ่งรออนุมัติไม่ทันงาน)
func (s *WBWPushService) SendToTokens(ctx context.Context, tokens []string, title, body string, data map[string]string) {
	if s.tokens == nil || len(tokens) == 0 {
		return
	}
	detached := context.WithoutCancel(ctx)
	goSafe("SendToTokens", func() {
		c, cancel := context.WithTimeout(detached, pushTimeout)
		defer cancel()
		if err := s.sendTokens(c, tokens, title, body, data); err != nil {
			slog.Error("ส่ง push ตามรายชื่อ token ไม่สำเร็จ", "err", err)
		}
	})
}

// sendTokens เหมือน sendUser ทุกขั้นตอน (ขอ token ครั้งเดียว ยิงแต่ละเครื่องพร้อมกัน
// เก็บกวาด token ตายผ่าน sendOne ตัวเดียวกัน) ต่างแค่ปลายทางเป็น token ที่ผู้เรียก
// ส่งมาตรงๆ แทนที่จะเดินทาง repo.XxxPushTargets จึงไม่มี badge ต่อเครื่องให้ผูก
// (SOS ไม่ใช่ตัวนับข้อความที่ยังไม่อ่าน) และ apns-push-type/interruption-level
// เป็นสองจุดที่ต่างจาก sendUser โดยตั้งใจ — ดูคอมเมนต์ที่ SendToTokens
func (s *WBWPushService) sendTokens(ctx context.Context, tokens []string, title, body string, data map[string]string) error {
	// ขอ access token ครั้งเดียวใช้ทั้งรอบ · TokenSource cache ให้อยู่แล้ว
	// ไม่ต้องขอใหม่ทุกเครื่อง
	tok, err := s.tokens.Token()
	if err != nil {
		return fmt.Errorf("ขอ access token ไม่ได้: %w", err)
	}

	var (
		mu      sync.Mutex
		invalid []string
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, pushConcurrency)

	// goSafe ด้วยเหตุผลเดียวกับใน sendChat/sendUser (ดูคอมเมนต์ที่ sendChat)
	for _, t := range tokens {
		wg.Add(1)
		goSafe("sendTokens.target", func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req := fcmRequest{Message: fcmMessage{
				Token:        t,
				Notification: &fcmNotification{Title: title, Body: body},
				Data:         data,
				APNS: &fcmAPNS{Headers: map[string]string{
					"apns-priority": "10",
					// alert (ไม่ใช่ background) + time-sensitive คือคู่ที่ทำให้ทะลุ Focus/
					// Do Not Disturb ได้จริงโดยไม่ต้องมี Critical Alerts entitlement
					"apns-push-type": "alert",
				}},
			}}
			req.Message.APNS.Payload.Aps = fcmAps{Sound: "default", InterruptionLevel: "time-sensitive"}

			dead, err := s.sendOne(ctx, tok, req)
			if err != nil {
				slog.Warn("push หนึ่งเครื่องล้มเหลว", "err", err)
				return
			}
			if dead {
				mu.Lock()
				invalid = append(invalid, t)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(invalid) > 0 {
		slog.Info("เก็บกวาด device token ที่ใช้ไม่ได้", "count", len(invalid))
		s.tokensReaped.Add(int64(len(invalid)))
		return s.repo.DeleteTokens(ctx, invalid)
	}
	return nil
}

// sendOne คืน dead=true เมื่อ FCM บอกว่า token นี้ใช้ไม่ได้แล้ว (ถอนแอป/ติดตั้งใหม่)
// ไม่ลบ token พวกนี้ทิ้ง = ยิง push ใส่เครื่องที่ไม่มีอยู่จริงไปเรื่อยๆ ทุกวัน
func (s *WBWPushService) sendOne(ctx context.Context, tok *oauth2.Token, payload fcmRequest) (dead bool, err error) {
	// นับที่นี่จุดเดียว — sendChat/sendUser/sendTokens ผ่านทางนี้หมด · นับ "พยายามส่ง"
	// ก่อนทุกอย่าง แล้วนับ "ล้ม" จาก err ตอนคืนค่า เพื่อให้สองเลขนี้เทียบกันได้เสมอ
	// ไม่ว่าจะออกทางไหน (token ตายไม่ใช่ความล้มเหลว — เป็นการเก็บกวาด นับแยก)
	s.sendsAttempted.Add(1)
	defer func() {
		if err != nil {
			s.sendsFailed.Add(1)
		}
	}()

	buf, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sendURL, bytes.NewReader(buf))
	if err != nil {
		return false, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	if resp.StatusCode == http.StatusOK {
		return false, nil
	}

	var fe fcmErrorResponse
	_ = json.Unmarshal(raw, &fe)
	for _, d := range fe.Error.Details {
		// UNREGISTERED = ถอนแอป/ติดตั้งใหม่ · INVALID_ARGUMENT = token เพี้ยน
		if d.ErrorCode == "UNREGISTERED" || d.ErrorCode == "INVALID_ARGUMENT" {
			return true, nil
		}
	}
	// 404 ที่ไม่มี details ก็ถือว่า token ตายแล้วเช่นกัน
	if resp.StatusCode == http.StatusNotFound {
		return true, nil
	}
	return false, fmt.Errorf("FCM ตอบ %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
}

// senderTitle — ชื่อผู้ส่งเป็นหัวข้อ notification · ไม่มีชื่อ (staff/admin ที่ไม่มี
// participant_profile) ใช้ข้อความกลางแทน
func senderTitle(msg model.Message) string {
	parts := []string{}
	for _, p := range []*string{msg.FirstName, msg.LastName} {
		if p != nil {
			if v := strings.TrimSpace(*p); v != "" {
				parts = append(parts, v)
			}
		}
	}
	if len(parts) == 0 {
		return "ข้อความใหม่"
	}
	return strings.Join(parts, " ")
}

// PushStats — สภาพของ push ตั้งแต่ boot สำหรับหน้า stats
//
// Enabled=false คือกรณีที่ตั้งใจให้เงียบ: ไม่มี GOOGLE_APPLICATION_CREDENTIALS แปลว่า
// ทุกคำสั่งส่งกลายเป็น no-op และแชทในแอปยังครบทุกอย่าง (ดูคอมเมนต์ที่ NewWBWPushService)
// ต้องแยกให้ออกจาก "เปิดอยู่แต่ส่งไม่ผ่านสักใบ" ซึ่งหน้าตาเหมือนกันเป๊ะจากฝั่งผู้ใช้
type PushStats struct {
	Enabled        bool  `json:"enabled"`
	SendsAttempted int64 `json:"sends_attempted"`
	// ล้มจริง ๆ (เน็ต/FCM ตอบ error ที่ไม่ใช่ token ตาย) · เทียบกับ attempted แล้วเข้าใกล้
	// กันเมื่อไรคือ push พังทั้งระบบ ไม่ใช่เครื่องใดเครื่องหนึ่ง
	SendsFailed int64 `json:"sends_failed"`
	// token ที่ถูกลบทิ้งเพราะ FCM บอกว่าใช้ไม่ได้แล้ว (ถอนแอป/ติดตั้งใหม่) — เป็นงานปกติ
	// ไม่ใช่ความผิดพลาด แต่ตัวเลขที่พุ่งผิดปกติแปลว่าเพิ่งลบ token ที่ยังดีอยู่ทิ้งไปเยอะ
	TokensReaped int64 `json:"tokens_reaped"`
}

func (s *WBWPushService) Stats() PushStats {
	return PushStats{
		Enabled:        s.tokens != nil,
		SendsAttempted: s.sendsAttempted.Load(),
		SendsFailed:    s.sendsFailed.Load(),
		TokensReaped:   s.tokensReaped.Load(),
	}
}
