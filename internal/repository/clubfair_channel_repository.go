package repository

import (
	"context"
	"errors"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAnnouncementNotFound = errors.New("clubfair: announcement not found")

type ClubFairChannelRepository struct {
	db *pgxpool.Pool
}

func NewClubFairChannelRepository(db *pgxpool.Pool) *ClubFairChannelRepository {
	return &ClubFairChannelRepository{db: db}
}

// List returns the channel oldest-first, with each post's reactions rolled up
// and `mine` resolved for [viewerID].
//
// Two queries rather than a join with an aggregate: a post's reaction chips are
// a small unordered set, and rolling them up in SQL would either duplicate every
// post row per reaction or need a nested aggregate that is harder to read than
// the loop below. Four posts and a handful of reactions — the second query is
// one index scan.
//
// Oldest-first because the channel is read like a chat: the newest post is at
// the bottom, where the reader is already looking.
func (r *ClubFairChannelRepository) List(ctx context.Context, viewerID int) ([]model.ClubFairAnnouncement, error) {
	rows, err := r.db.Query(ctx,
		`SELECT a.id, a.author_id, a.body, a.posted_at,
		        COALESCE(u.first_name || ' ' || u.surname, 'Student Union') AS author
		   FROM clubfair_announcement a
		   LEFT JOIN clubfair_users u ON u.id = a.author_id
		  WHERE a.deleted_at IS NULL
		  ORDER BY a.posted_at, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	posts := make([]model.ClubFairAnnouncement, 0)
	byID := map[int64]int{}
	for rows.Next() {
		var a model.ClubFairAnnouncement
		if err := rows.Scan(&a.ID, &a.AuthorID, &a.Body, &a.PostedAt, &a.Author); err != nil {
			return nil, err
		}
		a.Reactions = make([]model.ClubFairReaction, 0)
		byID[a.ID] = len(posts)
		posts = append(posts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return posts, nil
	}

	reactions, err := r.db.Query(ctx,
		`SELECT announcement_id, emoji, count(*) AS total,
		        bool_or(user_id = $1) AS mine
		   FROM clubfair_announcement_reaction
		  WHERE announcement_id = ANY (
		            SELECT id FROM clubfair_announcement WHERE deleted_at IS NULL)
		  GROUP BY announcement_id, emoji
		  ORDER BY total DESC, emoji`, viewerID)
	if err != nil {
		return nil, err
	}
	defer reactions.Close()

	for reactions.Next() {
		var id int64
		var re model.ClubFairReaction
		if err := reactions.Scan(&id, &re.Emoji, &re.Count, &re.Mine); err != nil {
			return nil, err
		}
		if idx, ok := byID[id]; ok {
			posts[idx].Reactions = append(posts[idx].Reactions, re)
		}
	}
	return posts, reactions.Err()
}

// Create posts an announcement. Only staff reach this — see the route.
func (r *ClubFairChannelRepository) Create(ctx context.Context, authorID int, body string) (*model.ClubFairAnnouncement, error) {
	var a model.ClubFairAnnouncement
	err := r.db.QueryRow(ctx,
		`INSERT INTO clubfair_announcement (author_id, body)
		 VALUES ($1, $2)
		 RETURNING id, author_id, body, posted_at`,
		authorID, body,
	).Scan(&a.ID, &a.AuthorID, &a.Body, &a.PostedAt)
	if err != nil {
		return nil, err
	}
	a.Reactions = make([]model.ClubFairReaction, 0)
	return &a, nil
}

// SoftDelete hides a post without losing it. A post two thousand students have
// read and reacted to should stop being shown rather than vanish from the record.
func (r *ClubFairChannelRepository) SoftDelete(ctx context.Context, id int64) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE clubfair_announcement SET deleted_at = now()
		  WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAnnouncementNotFound
	}
	return nil
}

// ToggleReaction adds the student's reaction or removes it, and reports the state
// it ended in.
//
// Delete-then-insert against the UNIQUE constraint rather than a read followed by
// a branch: two taps arriving together would both read "not reacted" and both try
// to insert, and one would fail. Here the first tap deletes nothing and inserts,
// the second deletes and inserts nothing.
func (r *ClubFairChannelRepository) ToggleReaction(
	ctx context.Context,
	announcementID int64,
	userID int,
	emoji string,
) (nowReacted bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM clubfair_announcement
		                 WHERE id = $1 AND deleted_at IS NULL)`,
		announcementID).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, ErrAnnouncementNotFound
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM clubfair_announcement_reaction
		  WHERE announcement_id = $1 AND user_id = $2 AND emoji = $3`,
		announcementID, userID, emoji)
	if err != nil {
		return false, err
	}

	if tag.RowsAffected() > 0 {
		return false, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO clubfair_announcement_reaction (announcement_id, user_id, emoji)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (announcement_id, user_id, emoji) DO NOTHING`,
		announcementID, userID, emoji); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Exists is used by the reaction route before it does any work.
func (r *ClubFairChannelRepository) Exists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM clubfair_announcement
		                 WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}
