package service

import (
	"context"
	"errors"
	"strings"
	"su-server/internal/model"
	"su-server/internal/repository"
	"unicode/utf8"
)

// The five the client offers. Enforced here as well so the column cannot become
// a free-text field by way of a crafted request — the CHECK on the table bounds
// the length, this bounds the set.
var ClubFairReactionPalette = []string{"👍", "❤️", "😂", "🎉", "👀"}

const maxAnnouncementRunes = 2000

var (
	ErrEmptyAnnouncement   = errors.New("clubfair: an announcement needs a body")
	ErrAnnouncementTooLong = errors.New("clubfair: announcement is too long")
	ErrUnsupportedReaction = errors.New("clubfair: not one of the five reactions")
)

type ClubFairChannelService struct {
	repo *repository.ClubFairChannelRepository
}

func NewClubFairChannelService(repo *repository.ClubFairChannelRepository) *ClubFairChannelService {
	return &ClubFairChannelService{repo: repo}
}

// List returns the channel with `mine` resolved for the caller.
func (s *ClubFairChannelService) List(ctx context.Context, viewerID int) ([]model.ClubFairAnnouncement, error) {
	return s.repo.List(ctx, viewerID)
}

func (s *ClubFairChannelService) Post(ctx context.Context, authorID int, body string) (*model.ClubFairAnnouncement, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, ErrEmptyAnnouncement
	}
	if utf8.RuneCountInString(trimmed) > maxAnnouncementRunes {
		return nil, ErrAnnouncementTooLong
	}
	return s.repo.Create(ctx, authorID, trimmed)
}

func (s *ClubFairChannelService) Delete(ctx context.Context, id int64) error {
	return s.repo.SoftDelete(ctx, id)
}

// React toggles one of the five, and reports the state it ended in so the client
// does not have to guess which way the tap went.
func (s *ClubFairChannelService) React(
	ctx context.Context,
	announcementID int64,
	userID int,
	emoji string,
) (nowReacted bool, err error) {
	allowed := false
	for _, candidate := range ClubFairReactionPalette {
		if candidate == emoji {
			allowed = true
			break
		}
	}
	if !allowed {
		return false, ErrUnsupportedReaction
	}
	return s.repo.ToggleReaction(ctx, announcementID, userID, emoji)
}
