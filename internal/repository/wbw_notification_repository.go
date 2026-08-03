package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WBWNotificationRepository struct {
	db *pgxpool.Pool
}

func NewWBWNotificationRepository(db *pgxpool.Pool) *WBWNotificationRepository {
	return &WBWNotificationRepository{db: db}
}

/* ---------- notifications ---------- */

func (r *WBWNotificationRepository) Create(ctx context.Context, n model.NotificationRequest, createdBy string) (*model.Notification, error) {
	var out model.Notification
	err := r.db.QueryRow(ctx, `
		INSERT INTO notification (type, title, body, level, audience, audience_id, created_by, expires_at)
		VALUES ($1, $2, $3, $4::noti_level, $5::audience_type, $6, $7, $8::timestamptz)
		RETURNING id, type, title, body, level::text, audience::text, audience_id,
		          created_by::text, created_at::text, expires_at::text`,
		deref(n.Type, "announcement"), n.Title, n.Body,
		deref(n.Level, "info"), deref(n.Audience, "all"), n.AudienceID,
		createdBy, n.ExpiresAt,
	).Scan(&out.ID, &out.Type, &out.Title, &out.Body, &out.Level, &out.Audience,
		&out.AudienceID, &out.CreatedBy, &out.CreatedAt, &out.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSent — delivered_count/read_count คืนเป็น string เพื่อให้ตรงกับของเดิม (node-pg ส่ง count เป็น string)
func (r *WBWNotificationRepository) ListSent(ctx context.Context) ([]model.NotificationSent, error) {
	rows, err := r.db.Query(ctx, `
		SELECT n.id, n.type, n.title, n.body, n.level::text, n.audience::text, n.audience_id,
		       n.created_at::text, n.expires_at::text,
		       COALESCE(u.display_name, u.username) AS creator_name,
		       (SELECT count(*) FROM notification_read nr
		         WHERE nr.notification_id = n.id AND nr.delivered_at IS NOT NULL)::text,
		       (SELECT count(*) FROM notification_read nr
		         WHERE nr.notification_id = n.id AND nr.read_at IS NOT NULL)::text
		  FROM notification n
		  LEFT JOIN wbw_user u ON u.user_id = n.created_by
		 ORDER BY n.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.NotificationSent{}
	for rows.Next() {
		var n model.NotificationSent
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Body, &n.Level, &n.Audience,
			&n.AudienceID, &n.CreatedAt, &n.ExpiresAt, &n.CreatorName,
			&n.DeliveredCount, &n.ReadCount); err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

// ListForUser กรองตาม audience ของผู้เรียก (staff/admin ไม่มี profile จึงเห็นแค่ all กับ user)
func (r *WBWNotificationRepository) ListForUser(ctx context.Context, userID string) ([]model.Notification, error) {
	rows, err := r.db.Query(ctx, `
		SELECT n.id, n.type, n.title, n.body, n.level::text, n.audience::text, n.audience_id,
		       n.created_by::text, n.created_at::text, n.expires_at::text, nr.read_at::text
		  FROM notification n
		  LEFT JOIN participant_profile p ON p.user_id = $1
		  LEFT JOIN notification_read  nr ON nr.notification_id = n.id AND nr.user_id = $1
		 WHERE (n.expires_at IS NULL OR n.expires_at > now())
		   AND ( n.audience = 'all'
		      OR (n.audience = 'user'   AND n.audience_id = $1::text)
		      OR (n.audience = 'group'  AND n.audience_id = p.group_id::text)
		      OR (n.audience = 'school' AND n.audience_id = p.school_id::text) )
		 ORDER BY n.created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Notification{}
	for rows.Next() {
		var n model.Notification
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Body, &n.Level, &n.Audience,
			&n.AudienceID, &n.CreatedBy, &n.CreatedAt, &n.ExpiresAt, &n.ReadAt); err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

// ListPublic — ประกาศสาธารณะ (audience='all') ที่ยังไม่หมดอายุ · ไม่ต้องล็อกอิน
// หน้า /announcements ที่เปิดดูได้ทั่วไปใช้อันนี้ (targeted ยังต้องล็อกอินผ่าน ListForUser)
func (r *WBWNotificationRepository) ListPublic(ctx context.Context) ([]model.NotificationPublic, error) {
	rows, err := r.db.Query(ctx, `
		SELECT n.id, n.type, n.title, n.body, n.level::text,
		       n.created_at::text, n.expires_at::text,
		       COALESCE(u.display_name, u.username) AS creator_name
		  FROM notification n
		  LEFT JOIN wbw_user u ON u.user_id = n.created_by
		 WHERE n.audience = 'all'
		   AND (n.expires_at IS NULL OR n.expires_at > now())
		 ORDER BY n.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.NotificationPublic{}
	for rows.Next() {
		var n model.NotificationPublic
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Body, &n.Level,
			&n.CreatedAt, &n.ExpiresAt, &n.CreatorName); err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

/* ---------- draft / preset (ตาราง notification_preset ร่วมกัน แยกด้วย kind) ---------- */

const presetSelect = `id, kind, name, title, body, level, audience, audience_id,
                      created_by::text, updated_at::text, created_at::text`

func scanPreset(row pgx.Row) (*model.Preset, error) {
	var p model.Preset
	err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.Title, &p.Body, &p.Level,
		&p.Audience, &p.AudienceID, &p.CreatedBy, &p.UpdatedAt, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetDraft คืน nil (ไม่ใช่ error) เมื่อยังไม่มี draft — frontend รับ null ได้
func (r *WBWNotificationRepository) GetDraft(ctx context.Context, userID string) (*model.Preset, error) {
	p, err := scanPreset(r.db.QueryRow(ctx,
		`SELECT `+presetSelect+` FROM notification_preset WHERE created_by = $1 AND kind = 'draft'`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// SaveDraft upsert ผ่าน partial unique index uniq_notif_draft (created_by) WHERE kind='draft'
func (r *WBWNotificationRepository) SaveDraft(ctx context.Context, userID string, req model.PresetRequest) (*model.Preset, error) {
	return scanPreset(r.db.QueryRow(ctx, `
		INSERT INTO notification_preset (kind, title, body, level, audience, audience_id, created_by)
		VALUES ('draft', $2, $3, $4, $5, $6, $1)
		ON CONFLICT (created_by) WHERE kind = 'draft' DO UPDATE SET
		  title = EXCLUDED.title, body = EXCLUDED.body, level = EXCLUDED.level,
		  audience = EXCLUDED.audience, audience_id = EXCLUDED.audience_id, updated_at = now()
		RETURNING `+presetSelect,
		userID, req.Title, req.Body, deref(req.Level, "info"), deref(req.Audience, "all"), req.AudienceID))
}

func (r *WBWNotificationRepository) DeleteDraft(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM notification_preset WHERE created_by = $1 AND kind = 'draft'`, userID)
	return err
}

func (r *WBWNotificationRepository) ListPresets(ctx context.Context, userID string) ([]model.Preset, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+presetSelect+` FROM notification_preset
		  WHERE created_by = $1 AND kind = 'preset' ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Preset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	return list, rows.Err()
}

func (r *WBWNotificationRepository) CreatePreset(ctx context.Context, userID string, req model.PresetRequest) (*model.Preset, error) {
	return scanPreset(r.db.QueryRow(ctx, `
		INSERT INTO notification_preset (kind, name, title, body, level, audience, audience_id, created_by)
		VALUES ('preset', $2, $3, $4, $5, $6, $7, $1)
		RETURNING `+presetSelect,
		userID, req.Name, req.Title, req.Body,
		deref(req.Level, "info"), deref(req.Audience, "all"), req.AudienceID))
}

// DeletePreset จำกัดเฉพาะของตัวเอง — ลบของคนอื่นจะเงียบๆ ไม่มีผล (ตามของเดิม)
func (r *WBWNotificationRepository) DeletePreset(ctx context.Context, userID string, id int64) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM notification_preset WHERE id = $1 AND created_by = $2 AND kind = 'preset'`, id, userID)
	return err
}

// MarkRead — ผู้ใช้กดอ่านประกาศ · upsert เพราะแถวอาจยังไม่เคยถูกสร้าง
// (notification_read สร้างตอน "ส่งถึง" ซึ่งของเดิมทำเฉพาะตอนยิง push)
//
// read_at ตั้งครั้งแรกแล้วไม่ทับ: เวลาที่อ่านครั้งแรกมีความหมาย ส่วนการเปิดซ้ำไม่มี
// delivered_at เติมให้ด้วยถ้ายังว่าง — อ่านได้แปลว่าถึงเครื่องแล้วแน่นอน
func (r *WBWNotificationRepository) MarkRead(ctx context.Context, userID string, notificationID int64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO notification_read (notification_id, user_id, delivered_at, read_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (notification_id, user_id) DO UPDATE
		   SET read_at = COALESCE(notification_read.read_at, now()),
		       delivered_at = COALESCE(notification_read.delivered_at, now())`,
		notificationID, userID)
	return err
}

func deref(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}
