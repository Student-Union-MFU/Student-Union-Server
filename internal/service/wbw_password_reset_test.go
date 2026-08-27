package service

import (
	"encoding/base64"
	"strings"
	"testing"
)

// อีเมลของเจ้าหน้าที่เป็นกุญแจสำรองของบัญชีที่มีสิทธิ์ — เทสต์ชุดนี้คุมสองอย่าง
// ที่พังเงียบได้: โดเมนนอกรายชื่อต้องไม่ผ่าน และค่าที่ผ่านต้องเป็นตัวพิมพ์เล็กเสมอ
// (ยูนีคในฐานเป็น lower(email) ถ้าคืนตัวพิมพ์ใหญ่ออกไป แถวที่เก็บกับ index จะคนละตัว)
func TestNormalizeEmailWithin(t *testing.T) {
	mfu := []string{"mfu.ac.th", "lamduan.mfu.ac.th"}

	tests := []struct {
		name    string
		in      string
		domains []string
		want    string
		wantErr bool
	}{
		{name: "อีเมลนักศึกษา", in: "6831503029@lamduan.mfu.ac.th", domains: mfu, want: "6831503029@lamduan.mfu.ac.th"},
		{name: "อีเมลบุคลากร", in: "somchai@mfu.ac.th", domains: mfu, want: "somchai@mfu.ac.th"},
		{name: "ตัวพิมพ์ใหญ่ถูกลดเป็นพิมพ์เล็ก", in: "Somchai@MFU.ac.th", domains: mfu, want: "somchai@mfu.ac.th"},
		{name: "ช่องว่างหน้าหลังถูกตัด", in: "  somchai@mfu.ac.th  ", domains: mfu, want: "somchai@mfu.ac.th"},
		{name: "รูปแบบมีชื่อกำกับ เอาเฉพาะที่อยู่", in: "สมชาย <somchai@mfu.ac.th>", domains: mfu, want: "somchai@mfu.ac.th"},

		{name: "โดเมนนอกรายชื่อ", in: "somchai@gmail.com", domains: mfu, wantErr: true},
		// โดเมนที่ "ลงท้ายเหมือน" ต้องไม่ผ่าน — เทียบทั้งก้อน ไม่ใช่ HasSuffix
		{name: "โดเมนปลอมที่ลงท้ายคล้ายกัน", in: "a@evil-mfu.ac.th", domains: mfu, wantErr: true},
		{name: "ซับโดเมนที่ไม่ได้อยู่ในรายชื่อ", in: "a@x.mfu.ac.th", domains: mfu, wantErr: true},
		{name: "ไม่ใช่อีเมล", in: "somchai", domains: mfu, wantErr: true},
		{name: "ว่าง", in: "", domains: mfu, wantErr: true},

		{name: "ดาวรับทุกโดเมน", in: "volunteer@gmail.com", domains: []string{"*"}, want: "volunteer@gmail.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEmailWithin(tt.in, tt.domains)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ควรถูกปฏิเสธ แต่ผ่านและได้ %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ควรผ่าน แต่ได้ error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ได้ %q ต้องการ %q", got, tt.want)
			}
		})
	}
}

func TestParseEmailDomains(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "ว่าง = อีเมลมหาวิทยาลัย", in: "", want: []string{"mfu.ac.th", "lamduan.mfu.ac.th"}},
		{name: "ช่องว่างล้วนก็คือว่าง", in: "   ", want: []string{"mfu.ac.th", "lamduan.mfu.ac.th"}},
		{name: "ตัดช่องว่างและลดเป็นพิมพ์เล็ก", in: " MFU.ac.th , example.com ", want: []string{"mfu.ac.th", "example.com"}},
		{name: "ข้ามช่องว่างเปล่าระหว่างจุลภาค", in: "a.com,,b.com", want: []string{"a.com", "b.com"}},
		{name: "ดาว", in: "*", want: []string{"*"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEmailDomains(tt.in)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("ได้ %v ต้องการ %v", got, tt.want)
			}
		})
	}
}

// เนื้ออีเมลเป็นภาษาไทยและมีลิงก์ที่ต้องกดได้ — สองอย่างนี้พังได้จากการเข้ารหัสผิด
// แล้วไม่มีใครรู้ เพราะไม่มีอะไรใน server ที่อ่านเมลที่ตัวเองส่งออกไป
func TestComposeMessage(t *testing.T) {
	s := &MailService{fromHeader: "WBW <noreply@mfu.ac.th>", from: "noreply@mfu.ac.th"}
	link := "https://wbw.example.ac.th/auth/reset?token=abc-123"
	msg := string(s.compose("6831503029@lamduan.mfu.ac.th", resetMailSubject, resetMailBody("6831503029", link)))

	head, body, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatal("ไม่มีบรรทัดว่างคั่นระหว่างหัวจดหมายกับเนื้อ")
	}

	// หัวจดหมายต้องไม่มี UTF-8 ดิบหลุดออกไป — ต้องผ่าน RFC 2047 มาแล้ว
	for _, line := range strings.Split(head, "\r\n") {
		for _, r := range line {
			if r > 127 {
				t.Fatalf("หัวจดหมายมีอักขระนอก ASCII ที่ยังไม่ได้เข้ารหัส: %q", line)
			}
		}
	}
	if !strings.Contains(head, "Content-Transfer-Encoding: base64") {
		t.Error("ไม่ได้ประกาศ Content-Transfer-Encoding: base64")
	}
	if !strings.Contains(head, `charset="UTF-8"`) {
		t.Error("ไม่ได้ประกาศ charset UTF-8")
	}

	// RFC 2045: บรรทัด base64 ยาวได้ไม่เกิน 76 ตัว
	for _, line := range strings.Split(strings.TrimRight(body, "\r\n"), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("บรรทัด base64 ยาว %d ตัว (เกิน 76)", len(line))
		}
	}

	decoded := decodeBase64Body(t, body)
	if !strings.Contains(decoded, link) {
		t.Errorf("ลิงก์รีเซ็ตหายไปจากเนื้อจดหมาย:\n%s", decoded)
	}
	if !strings.Contains(decoded, "30 นาที") {
		t.Errorf("ไม่ได้บอกอายุของลิงก์ตาม resetTTL:\n%s", decoded)
	}
}

func decodeBase64Body(t *testing.T, body string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil {
		t.Fatalf("ถอด base64 ของเนื้อจดหมายไม่ได้: %v", err)
	}
	return string(raw)
}
