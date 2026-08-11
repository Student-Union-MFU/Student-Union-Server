package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

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
}

func NewWBWAuthService(repo *repository.WBWAuthRepository, tokens *WBWTokenService) *WBWAuthService {
	return &WBWAuthService{repo: repo, tokens: tokens}
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
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, err
	}
	var major *string
	if m := strings.TrimSpace(req.Major); m != "" {
		major = &m
	}
	user, err := s.repo.RegisterStaff(ctx, username, string(hash), *req.SchoolID, major, req.StaffRole)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, ErrDuplicateUser
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
