package service

import (
	"context"

	"su-server/internal/model"
	"su-server/internal/repository"
)

type WBWGroupService struct {
	repo   *repository.WBWGroupRepository
	events *ChatEvents
}

func NewWBWGroupService(repo *repository.WBWGroupRepository, events *ChatEvents) *WBWGroupService {
	return &WBWGroupService{repo: repo, events: events}
}

func (s *WBWGroupService) Members(ctx context.Context, groupID int) (*model.GroupMembersResponse, error) {
	members, err := s.repo.Members(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return &model.GroupMembersResponse{Members: members, Count: len(members)}, nil
}

func (s *WBWGroupService) MembersIndex(ctx context.Context) ([]model.GroupMemberIndex, error) {
	return s.repo.MembersIndex(ctx)
}

// Join — เข้ากลุ่ม แล้วปลุก long-poll ของกลุ่มนั้นให้ member_count กับ cursor สดทันที
// (ไม่ปลุก = คนที่เปิดจอแชทค้างอยู่จะเห็น "อ่านแล้ว N" ผิดจนกว่าจะครบรอบ 25 วิ)
func (s *WBWGroupService) Join(ctx context.Context, userID string, groupID int) error {
	if err := s.repo.Join(ctx, userID, groupID); err != nil {
		return err
	}
	s.events.NotifyGroup(ctx, groupID, "")
	return nil
}

// Leave — ออกจากกลุ่ม แล้วปลุกกลุ่มเดิม เพราะจำนวนสมาชิกที่ใช้คิด "อ่านแล้ว N" เปลี่ยนไป
func (s *WBWGroupService) Leave(ctx context.Context, userID string) error {
	prevGroup, err := s.repo.Leave(ctx, userID)
	if err != nil {
		return err
	}
	s.events.NotifyGroup(ctx, prevGroup, "")
	return nil
}
