package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"su-server/internal/model"
	"su-server/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

// ข้อผิดพลาดเชิงธุรกิจ — handler แปลงเป็น status + ข้อความไทย
var (
	ErrBadStudentID    = errors.New("bad student id")
	ErrShortPassword   = errors.New("short password")
	ErrHasChronic      = errors.New("has chronic condition")
	ErrDuplicateUser   = errors.New("duplicate user")
	ErrBadCredentials  = errors.New("bad credentials")
	ErrMissingFields   = errors.New("missing fields")
	ErrPendingApproval = errors.New("pending approval")
	ErrBadStaffRole    = errors.New("bad staff role")
	ErrBadEmail        = errors.New("bad email")
	ErrEmailTaken      = errors.New("email taken")
	// ตั๋วรีเซ็ตใช้ไม่ได้ — ไม่มีจริง/หมดอายุ/ใช้ไปแล้ว รวมเป็นอันเดียวโดยตั้งใจ
	ErrBadResetToken = errors.New("bad reset token")
	// ที่นั่งเต็ม — โควตาผู้เข้าร่วมทั้งงาน (staff/admin ไม่นับ) ดู migration 000021
	ErrEventFull = errors.New("event full")
)

// หน้าที่ของเจ้าหน้าที่ — ต้องตรงกับ enum staff_role ใน DB (migration 000009)
// และ STAFF_ROLES ฝั่งเว็บ (components/register/mfu-data.ts)
var validStaffRoles = []string{
	"registration", "checkpoint", "backstage", "security", "medical",
	"welfare", "logistics", "media", "guide", "other",
}

func isValidStaffRole(r string) bool {
	for _, v := range validStaffRoles {
		if v == r {
			return true
		}
	}
	return false
}

// นักศึกษาชั้นปีที่ 1 — 10 หลัก ขึ้นต้น 693
var studentIDRe = regexp.MustCompile(`^693\d{7}$`)

// bcrypt cost 10 ให้ตรงกับ Express เดิม (bcryptjs hash(pw, 10))
// hash ที่สร้างจากฝั่งเดิมจึงยังใช้ล็อกอินผ่าน Go ได้
const bcryptCost = 10

type WBWAuthService struct {
	repo   *repository.WBWAuthRepository
	tokens *WBWTokenService
	mail   *MailService
	// ฐานของลิงก์รีเซ็ตที่ส่งไปในอีเมล เช่น https://wbw.example.ac.th — ต้องมาจาก
	// env ฝั่งเซิร์ฟเวอร์ ไม่ใช่จาก request: ถ้าเอา Host header มาต่อ ใครก็ส่งลิงก์
	// รีเซ็ตของเหยื่อที่ชี้ไปเว็บตัวเองได้ แค่ยิง /auth/forgot พร้อม Host ปลอม
	webBaseURL string
}

func NewWBWAuthService(
	repo *repository.WBWAuthRepository,
	tokens *WBWTokenService,
	mail *MailService,
	webBaseURL string,
) *WBWAuthService {
	return &WBWAuthService{
		repo:       repo,
		tokens:     tokens,
		mail:       mail,
		webBaseURL: strings.TrimRight(webBaseURL, "/"),
	}
}

func (s *WBWAuthService) Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error) {
	sid := strings.TrimSpace(req.StudentID)
	if sid == "" {
		sid = strings.TrimSpace(req.Username)
	}
	if !studentIDRe.MatchString(sid) {
		return nil, ErrBadStudentID
	}
	if len(req.Password) < 8 {
		return nil, ErrShortPassword
	}
	// กิจกรรมรับเฉพาะผู้ไม่มีโรคประจำตัว
	if len(req.Health.ChronicConditions) > 0 {
		return nil, ErrHasChronic
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, err
	}

	// medical.birthdate มาก่อน profile.date_of_birth
	birthdate := req.Medical.Birthdate
	if birthdate == nil || *birthdate == "" {
		birthdate = req.Profile.DateOfBirth
	}
	if birthdate != nil && *birthdate == "" {
		birthdate = nil
	}

	user, err := s.repo.Register(ctx, req, sid, string(hash), birthdate)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, ErrDuplicateUser
		}
		if errors.Is(err, repository.ErrFull) {
			return nil, ErrEventFull
		}
		return nil, err
	}

	token, err := s.tokens.Sign(user.UserID, user.Role, user.Username)
	if err != nil {
		return nil, err
	}
	return &model.AuthResponse{User: *user, Token: token}, nil
}

// RegisterStaff สมัครเป็นเจ้าหน้าที่ — สร้างบัญชีสถานะ pending ไม่คืน token
// (ล็อกอินไม่ได้จนกว่าแอดมินจะอนุมัติ)
func (s *WBWAuthService) RegisterStaff(ctx context.Context, req model.StaffRegisterRequest) (*model.AuthUser, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, ErrMissingFields
	}
	if len(req.Password) < 8 {
		return nil, ErrShortPassword
	}
	// สำนักวิชาบังคับ · สาขาไม่บังคับ (บางสำนักไม่มีให้เลือกในฟอร์ม)
	if req.SchoolID == nil {
		return nil, ErrMissingFields
	}
	if !isValidStaffRole(req.StaffRole) {
		return nil, ErrBadStaffRole
	}
	// อีเมลบังคับ — เป็นช่องทางเดียวที่เจ้าหน้าที่จะกู้รหัสผ่านเองได้ (participant
	// ใช้ student_id คำนวณเอาได้ เจ้าหน้าที่ไม่มี) ดู migration 000036
	email, err := normalizeStaffEmail(req.Email)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, err
	}
	var major *string
	if m := strings.TrimSpace(req.Major); m != "" {
		major = &m
	}
	user, err := s.repo.RegisterStaff(ctx, username, string(hash), email, *req.SchoolID, major, req.StaffRole)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, ErrDuplicateUser
		}
		if errors.Is(err, repository.ErrDuplicateEmail) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return user, nil
}

// Capacity — สถานะที่นั่งของงาน สำหรับหน้าสมัคร (เปิดสาธารณะ ไม่ต้องล็อกอิน)
func (s *WBWAuthService) Capacity(ctx context.Context) (*model.Capacity, error) {
	return s.repo.Capacity(ctx)
}

func (s *WBWAuthService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	if req.Username == "" || req.Password == "" {
		return nil, ErrMissingFields
	}

	user, hash, status, err := s.repo.FindByUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	// user ไม่มี กับ รหัสผิด ตอบเหมือนกัน — กัน enumeration
	if user == nil {
		return nil, ErrBadCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, ErrBadCredentials
	}
	// staff ที่สมัครเองแต่ยังไม่ได้รับอนุมัติ ล็อกอินไม่ได้
	// (participant + บัญชีที่แอดมินสร้างเอง เป็น 'approved' อยู่แล้ว จึงผ่านหมด)
	if status != "approved" {
		return nil, ErrPendingApproval
	}

	token, err := s.tokens.Sign(user.UserID, user.Role, user.Username)
	if err != nil {
		return nil, err
	}
	return &model.AuthResponse{User: *user, Token: token}, nil
}

/* ---------- ลืมรหัสผ่าน (migration 000036) ---------- */

const (
	// อายุของลิงก์ · สั้นพอที่ลิงก์ในกล่องจดหมายที่ถูกอ่านทีหลังจะไม่เป็นกุญแจค้างอยู่
	// ยาวพอให้คนที่เปิดเมลบนมือถือแล้วเดินไปหาคอมพิวเตอร์ทำทัน
	resetTTL = 30 * time.Minute
	// ขอลิงก์ได้กี่ใบต่อบัญชีต่อชั่วโมง — กันคนเอา endpoint นี้ยิงเมลใส่คนอื่นรัว ๆ
	// (ปลายทางคืออีเมลของ "เจ้าของบัญชี" เสมอ ผู้ขอเลือกไม่ได้ จึงเป็นการก่อกวน
	//  ไม่ใช่การขโมย แต่ก็ยังทำให้ relay เรามีสิทธิ์โดนแบนได้)
	resetMaxPerHour = 3
	// 256 บิต — ไม่มีอะไรให้เดา จึงเก็บลงฐานเป็น sha256 เปล่า ๆ ได้ ไม่ต้อง bcrypt
	resetTokenBytes = 32
)

// RequestPasswordReset ออกตั๋วรีเซ็ตแล้วส่งลิงก์ไปที่อีเมลของเจ้าของบัญชี
//
// ⚠ คืน nil ทุกกรณีที่ "ส่งไม่ได้" — ไม่มีบัญชีนี้ / บัญชีไม่มีอีเมล / เจ้าหน้าที่ยังไม่
// ผ่านการอนุมัติ / ขอถี่เกินโควตา · ฝั่ง handler จึงตอบ 204 เหมือนกันหมด เพราะ
// endpoint นี้เปิดสาธารณะ: ถ้าตอบต่างกัน มันจะกลายเป็นเครื่องมือไล่เช็คว่ารหัส
// นักศึกษาไหนสมัครงานนี้ไว้บ้าง (เหตุผลเดียวกับที่ Login ตอบ 401 เหมือนกันหมด)
// error ที่คืนจริงมีแค่กรณีฐานข้อมูลพัง ซึ่งเป็น 500 ไม่ใช่คำใบ้เรื่องบัญชี
func (s *WBWAuthService) RequestPasswordReset(ctx context.Context, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil
	}

	userID, email, status, err := s.repo.FindResetTarget(ctx, username)
	if err != nil {
		return err
	}
	switch {
	case userID == "":
		slog.Info("ขอรีเซ็ตรหัสผ่าน: ไม่มีบัญชีนี้", "username", username)
		return nil
	case email == "":
		// เจ้าหน้าที่ที่สมัครไว้ก่อน migration 000036 — ไม่มีอีเมลและคำนวณไม่ได้
		// ทางเดียวของคนกลุ่มนี้คือให้แอดมินตั้งรหัสให้ผ่าน /admin/users/{id}/password
		slog.Warn("ขอรีเซ็ตรหัสผ่าน: บัญชีนี้ไม่มีอีเมล ต้องให้แอดมินตั้งให้", "username", username)
		return nil
	case status != "approved":
		slog.Info("ขอรีเซ็ตรหัสผ่าน: บัญชียังรออนุมัติ", "username", username)
		return nil
	}

	n, err := s.repo.CountRecentResets(ctx, userID, time.Now().Add(-time.Hour))
	if err != nil {
		return err
	}
	if n >= resetMaxPerHour {
		slog.Warn("ขอรีเซ็ตรหัสผ่านถี่เกินโควตา", "username", username, "in_last_hour", n)
		return nil
	}

	raw := make([]byte, resetTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	// เก็บแฮช ไม่ใช่ตัว token — ตั้งแต่บรรทัดนี้ไป ตัวจริงมีอยู่ที่เดียวคือในอีเมล
	sum := sha256.Sum256([]byte(token))
	if err := s.repo.InsertPasswordReset(ctx, userID, sum[:], time.Now().Add(resetTTL)); err != nil {
		return err
	}

	link := s.webBaseURL + "/auth/reset?token=" + token
	// ส่งแบบไม่รอ และไม่ผูกกับ ctx ของ request — request นี้ตอบ 204 ไปแล้วไม่ว่า
	// เมลจะออกหรือไม่ (ถ้ารอ เวลาที่ relay อืดจะกลายเป็นคำใบ้ว่าบัญชีมีจริง)
	// MailService.Send มี deadline ของตัวเอง goroutine นี้จึงไม่ค้าง
	go func() {
		if err := s.mail.Send(email, resetMailSubject, resetMailBody(username, link)); err != nil {
			slog.Error("ส่งอีเมลรีเซ็ตรหัสผ่านไม่สำเร็จ", "username", username, "err", err)
		}
	}()
	return nil
}

// ResetPassword แลกตั๋วเป็นรหัสผ่านใหม่
//
// bcrypt ทำก่อนรู้ว่าตั๋วใช้ได้จริงหรือเปล่า (ต้องมีแฮชไว้ให้ repository เขียนใน
// transaction เดียวกับที่กินตั๋ว) คนที่ยิงตั๋วมั่วจึงเผา CPU ได้ — รับได้ เพราะ
// route นี้อยู่ใต้ throttle ตัวเดียวกับ /auth/login ซึ่งจำกัดจำนวนที่ทำพร้อมกันไว้แล้ว
// และการยิง /auth/login มั่วก็มีต้นทุนเท่ากันเป๊ะ
func (s *WBWAuthService) ResetPassword(ctx context.Context, token, password string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrBadResetToken
	}
	if len(password) < 8 {
		return ErrShortPassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(token))
	username, err := s.repo.ConsumePasswordReset(ctx, sum[:], string(hash))
	if err != nil {
		if errors.Is(err, repository.ErrResetTokenInvalid) {
			return ErrBadResetToken
		}
		return err
	}
	// ⚠ token ที่ออกไปก่อนหน้านี้ (JWT ของ session ที่ยังเปิดอยู่) ไม่ถูกยกเลิก —
	// WBWTokenService เซ็นอย่างเดียว ไม่มีบัญชีดำ ใครที่ถือ token เก่าของบัญชีนี้
	// จึงยังใช้ต่อได้จนหมดอายุตาม JWT_EXPIRY · ถ้าต้องปิดช่องนี้ ต้องเพิ่มคอลัมน์
	// password_changed_at แล้วให้ RequireAuth เทียบกับ iat ของ token
	slog.Info("ตั้งรหัสผ่านใหม่ผ่านลิงก์ในอีเมลสำเร็จ", "username", username)
	return nil
}

const resetMailSubject = "ตั้งรหัสผ่านใหม่ — เดินรอบดอย (WBW)"

func resetMailBody(username, link string) string {
	return fmt.Sprintf(`สวัสดีครับ/ค่ะ คุณ %s

มีคนขอตั้งรหัสผ่านใหม่ให้บัญชีเดินรอบดอย (WBW) ของคุณ
เปิดลิงก์ข้างล่างเพื่อตั้งรหัสผ่านใหม่ — ใช้ได้ครั้งเดียว ภายใน %d นาที

%s

ถ้าคุณไม่ได้เป็นคนขอ ไม่ต้องทำอะไรเลย รหัสผ่านเดิมยังใช้ได้ตามปกติ
และลิงก์นี้จะหมดอายุไปเอง

อีเมลฉบับนี้ส่งอัตโนมัติ กรุณาอย่าตอบกลับ
— ทีมงานเดินรอบดอย`, username, int(resetTTL.Minutes()), link)
}

// โดเมนอีเมลที่รับสำหรับบัญชีเจ้าหน้าที่ · ตั้งผ่าน WBW_STAFF_EMAIL_DOMAINS
// (คั่นด้วยจุลภาค, "*" = รับทุกโดเมน) ไม่ตั้ง = เฉพาะอีเมลมหาวิทยาลัย
//
// ที่ต้องจำกัด เพราะอีเมลคือกุญแจสำรองของบัญชีที่มีสิทธิ์เจ้าหน้าที่ ปล่อยให้ใช้
// อีเมลอะไรก็ได้ = ใครก็ตามที่ยึดอีเมลส่วนตัวของเจ้าหน้าที่ได้ ก็ยึดบัญชีเจ้าหน้าที่
// ต่อได้ทันที · แต่เปิดช่องปรับไว้ เผื่อมีอาสาสมัครนอกมหาวิทยาลัยที่ต้องรับจริง ๆ
// (ทรงเดียวกับ CLUBFAIR_INTAKE_PREFIXES)
var staffEmailDomains = sync.OnceValue(func() []string {
	return parseEmailDomains(os.Getenv("WBW_STAFF_EMAIL_DOMAINS"))
})

// parseEmailDomains แปลงค่า env เป็นรายชื่อโดเมน · ว่าง = ค่าเริ่มต้น (อีเมลมหาวิทยาลัย)
func parseEmailDomains(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "mfu.ac.th,lamduan.mfu.ac.th"
	}
	out := []string{}
	for _, d := range strings.Split(raw, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// normalizeStaffEmail ตรวจรูปแบบ + โดเมน แล้วคืนอีเมลตัวพิมพ์เล็ก
// (ยูนีคในฐานเป็น lower(email) — ถ้าไม่ทำให้ตรงกันตั้งแต่ตรงนี้ จะได้สองแถวที่
//
//	ต่างกันแค่ตัวพิมพ์ในคอลัมน์ แล้วอีเมลที่ส่งจริงเป็นคนละตัวกับที่ index กันไว้)
func normalizeStaffEmail(raw string) (string, error) {
	return normalizeEmailWithin(raw, staffEmailDomains())
}

// normalizeEmailWithin แยกออกมาจาก normalizeStaffEmail เพื่อให้เทสต์ส่งรายชื่อโดเมน
// เข้ามาเองได้ — staffEmailDomains เป็น OnceValue ที่อ่าน env ครั้งเดียวตลอดโปรเซส
// เทสต์ที่ t.Setenv แล้วเรียกทีหลังจะได้ค่าที่ถูกจำไว้จากเทสต์ก่อนหน้า ไม่ใช่ค่าที่ตั้ง
func normalizeEmailWithin(raw string, domains []string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return "", ErrBadEmail
	}
	email := strings.ToLower(addr.Address)
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "", ErrBadEmail
	}
	domain := email[at+1:]
	for _, d := range domains {
		if d == "*" || d == domain {
			return email, nil
		}
	}
	return "", ErrBadEmail
}
