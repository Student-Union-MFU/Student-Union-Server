package service

import (
	"context"
	"errors"
	"strings"

	"su-server/internal/model"
	"su-server/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrBadRole       = errors.New("bad role")
	ErrBadCheckpoint = errors.New("bad checkpoint type")
	ErrSelfRole      = errors.New("cannot change own role")
	ErrSelfDelete    = errors.New("cannot delete self")
	ErrEmptyName     = errors.New("empty name")
)

var validCheckpointTypes = []string{"activity", "restroom", "welfare", "recreation", "service"}

type WBWAdminService struct {
	admin *repository.WBWAdminRepository
	cp    *repository.WBWCheckpointRepository
}

func NewWBWAdminService(admin *repository.WBWAdminRepository, cp *repository.WBWCheckpointRepository) *WBWAdminService {
	return &WBWAdminService{admin: admin, cp: cp}
}

/* ---------- passthrough ---------- */

func (s *WBWAdminService) ListSchools(ctx context.Context) ([]model.School, error) {
	return s.admin.ListSchools(ctx)
}

func (s *WBWAdminService) Dashboard(ctx context.Context) (*model.DashboardStats, error) {
	return s.admin.Dashboard(ctx)
}

func (s *WBWAdminService) ListParticipants(ctx context.Context) ([]model.Participant, error) {
	return s.admin.ListParticipants(ctx)
}

func (s *WBWAdminService) ParticipantDetail(ctx context.Context, id string) (*model.ParticipantDetail, error) {
	return s.admin.ParticipantDetail(ctx, id)
}

func (s *WBWAdminService) ListGroups(ctx context.Context) ([]model.Group, error) {
	return s.admin.ListGroups(ctx)
}

// UpdateOwnPhoto — เปลี่ยนรูปโปรไฟล์ตัวเอง · ส่ง photo_url เป็น null = ลบรูป
func (s *WBWAdminService) UpdateOwnPhoto(ctx context.Context, userID string, photoURL *string) error {
	return s.admin.UpdateOwnPhoto(ctx, userID, photoURL)
}

func (s *WBWAdminService) ListLogs(ctx context.Context) ([]model.AdminLog, error) {
	return s.admin.ListLogs(ctx)
}

func (s *WBWAdminService) Log(ctx context.Context, actorID, actorName, action, detail string) {
	s.admin.LogAction(ctx, actorID, actorName, action, detail)
}

/* ---------- participants ---------- */

func (s *WBWAdminService) UpdateParticipant(ctx context.Context, id string, patch model.ParticipantPatch) (*model.Participant, error) {
	if patch.StudentID != nil && !studentIDRe.MatchString(*patch.StudentID) {
		return nil, ErrBadStudentID
	}
	return s.admin.UpdateParticipant(ctx, id, patch)
}

func (s *WBWAdminService) ResetParticipantPassword(ctx context.Context, id, password string) (string, error) {
	if len(password) < 8 {
		return "", ErrShortPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return s.admin.ResetParticipantPassword(ctx, id, string(hash))
}

func (s *WBWAdminService) DeleteParticipant(ctx context.Context, id string) (string, error) {
	return s.admin.DeleteParticipant(ctx, id)
}

/* ---------- checkpoints ---------- */

func (s *WBWAdminService) ListCheckpoints(ctx context.Context) ([]model.Checkpoint, error) {
	return s.cp.List(ctx)
}

func (s *WBWAdminService) BasesOverview(ctx context.Context) ([]model.BaseOverview, error) {
	return s.cp.BasesOverview(ctx)
}

func (s *WBWAdminService) CreateCheckpoint(ctx context.Context, req model.CheckpointRequest) (*model.Checkpoint, error) {
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" {
		return nil, ErrEmptyName
	}
	// type ที่ไม่รู้จักตกเป็น activity เงียบๆ (ตามของเดิม)
	cpType := "activity"
	if req.Type != nil && isValidCheckpointType(*req.Type) {
		cpType = *req.Type
	}
	return s.cp.Create(ctx, name, cleanOptional(req.NameEn), cpType, req.Sequence)
}

func (s *WBWAdminService) UpdateCheckpoint(ctx context.Context, id int, req model.CheckpointRequest) (*model.CheckpointPatched, error) {
	var name *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			return nil, ErrEmptyName
		}
		name = &trimmed
	}
	if req.Type != nil && !isValidCheckpointType(*req.Type) {
		return nil, ErrBadCheckpoint
	}
	return s.cp.Update(ctx, id, name, cleanOptional(req.NameEn), req.Type, req.Sequence)
}

// cleanOptional — trim ช่องว่าง · ว่าง = nil (เก็บเป็น NULL ไม่ใช่สตริงว่าง)
func cleanOptional(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func (s *WBWAdminService) DeleteCheckpoint(ctx context.Context, id int) (string, error) {
	return s.cp.Delete(ctx, id)
}

func (s *WBWAdminService) AssignStaff(ctx context.Context, checkpointID int, userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", ErrMissingFields
	}
	return s.cp.AssignStaff(ctx, checkpointID, userID)
}

func (s *WBWAdminService) RemoveStaff(ctx context.Context, checkpointID int, userID string) (bool, error) {
	return s.cp.RemoveStaff(ctx, checkpointID, userID)
}

/* ---------- staff / admin accounts ---------- */

func (s *WBWAdminService) ListUsers(ctx context.Context) ([]model.AdminUser, error) {
	return s.cp.ListUsers(ctx)
}

func (s *WBWAdminService) CreateUser(ctx context.Context, req model.CreateAdminUserRequest) (*model.AdminUser, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, ErrMissingFields
	}
	if len(req.Password) < 8 {
		return nil, ErrShortPassword
	}
	if req.Role != "staff" && req.Role != "admin" {
		return nil, ErrBadRole
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, err
	}
	u, err := s.cp.CreateUser(ctx, username, string(hash), req.Role, req.DisplayName)
	if errors.Is(err, repository.ErrDuplicate) {
		return nil, ErrDuplicateUser
	}
	return u, err
}

// UpdateUser — กันแอดมินถอดสิทธิ์ตัวเอง (จะเหลือระบบไม่มีแอดมิน)
func (s *WBWAdminService) UpdateUser(ctx context.Context, id, callerID string, req model.UpdateAdminUserRequest) (*model.AdminUser, error) {
	if req.Role != nil {
		if *req.Role != "staff" && *req.Role != "admin" {
			return nil, ErrBadRole
		}
		if id == callerID && *req.Role != "admin" {
			return nil, ErrSelfRole
		}
	}
	return s.cp.UpdateUser(ctx, id, req.DisplayName, req.Role)
}

func (s *WBWAdminService) SetUserPassword(ctx context.Context, id, password string) error {
	if len(password) < 8 {
		return ErrShortPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	return s.cp.SetUserPassword(ctx, id, string(hash))
}

func (s *WBWAdminService) DeleteUser(ctx context.Context, id, callerID string) (string, error) {
	if id == callerID {
		return "", ErrSelfDelete
	}
	return s.cp.DeleteUser(ctx, id)
}

/* ---------- staff requests (สมัครเอง รออนุมัติ) ---------- */

func (s *WBWAdminService) ListStaffRequests(ctx context.Context) ([]model.StaffRequest, error) {
	return s.cp.ListStaffRequests(ctx)
}

func (s *WBWAdminService) ApproveStaff(ctx context.Context, id string) (string, error) {
	return s.cp.ApproveStaffRequest(ctx, id)
}

func (s *WBWAdminService) RejectStaff(ctx context.Context, id string) (string, error) {
	return s.cp.RejectStaffRequest(ctx, id)
}

func isValidCheckpointType(t string) bool {
	for _, v := range validCheckpointTypes {
		if v == t {
			return true
		}
	}
	return false
}
