package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/* ============================================================
   มุมมองแอดมินของเคส SOS — ต่างจาก StaffFeed ที่เจ้าหน้าที่ใช้หน้างาน

   StaffFeed ตอบเฉพาะเคสที่ "ยังต้องทำอะไรกับมัน" (เปิดอยู่ หรือเพิ่งปิดไม่เกิน
   30 นาที) เพราะมันคือหน้าจอที่เจ้าหน้าที่จ้องระหว่างงาน — ประวัติเก่าที่ปิดไป
   แล้วมีแต่จะบังเคสที่ยังเปิดอยู่

   แผงแอดมินถามคนละคำถาม: "ทั้งงานมีกี่เคส ใครกด เมื่อไหร่ เพราะอะไร จบยังไง"
   ซึ่งต้องเห็นทุกเคสรวมที่ปิดไปแล้ว จึงเป็นคนละ query ไม่ใช่การไปคลาย filter
   ของ StaffFeed — คลายเมื่อไหร่หน้าจอหน้างานก็พังตามไปด้วย
   ============================================================ */

// ErrSOSAlreadyOpen — เปิดเคสซ้ำให้คนที่มีเคสเปิดค้างอยู่แล้วไม่ได้
//
// มาจาก unique partial index sos_one_open_per_user · เจอได้ทั้งตอนสร้างเคสใหม่
// และตอน "เปิดเคสที่ปิดไปแล้วขึ้นมาใหม่" ซึ่งเป็นเหตุผลที่ error นี้อยู่ตรงนี้
// ไม่ใช่ในไฟล์ SOS หลัก
var ErrSOSAlreadyOpen = errors.New("sos already open for this participant")

// AdminList — ทุกเคสของทั้งงาน ใหม่สุดก่อน
//
// ใช้ sosStaffSelect ตัวเดียวกับหน้างานโดยตั้งใจ รวมถึงเงื่อนไขปิดบังข้อมูลสุขภาพ
// (ยินยอม + เคสยังเปิด + ไม่ใช่กดแทนคนอื่น) — แอดมินไม่ได้รับสิทธิ์เห็นประวัติ
// สุขภาพของทุกคนย้อนหลังเพียงเพราะเป็นแอดมิน สิ่งที่แอดมินได้เพิ่มคือ "เห็นทุกเคส"
// ไม่ใช่ "เห็นทุกฟิลด์"
func (r *WBWSOSRepository) AdminList(ctx context.Context, limit int) ([]model.SOSStaffCase, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.db.Query(ctx, sosStaffSelect+` ORDER BY s.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.SOSStaffCase{}
	for rows.Next() {
		var c model.SOSStaffCase
		if err := rows.Scan(&c.ID, &c.ForOther, &c.Lat, &c.Lng, &c.AccuracyM, &c.LocSource,
			&c.CheckpointID, &c.CheckpointName, &c.Message, &c.Resolved, &c.ResolveReason,
			&c.AckedAt, &c.AckedByName, &c.CreatedAt, &c.GroupID, &c.Severity, &c.Escalated,
			&c.UpdatedAt,
			&c.ParticipantID, &c.FirstName, &c.LastName, &c.Bib, &c.GroupNumber,
			&c.ContactPhone, &c.EmergencyName, &c.EmergencyPh,
			&c.BloodType, &c.HealthNotes); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// AdminGet — เคสเดียวในรูปแบบเดียวกับ AdminList (ใช้ตอบกลับหลังแก้ไข)
func (r *WBWSOSRepository) AdminGet(ctx context.Context, id int64) (*model.SOSStaffCase, error) {
	var c model.SOSStaffCase
	err := r.db.QueryRow(ctx, sosStaffSelect+` WHERE s.id = $1`, id).Scan(
		&c.ID, &c.ForOther, &c.Lat, &c.Lng, &c.AccuracyM, &c.LocSource,
		&c.CheckpointID, &c.CheckpointName, &c.Message, &c.Resolved, &c.ResolveReason,
		&c.AckedAt, &c.AckedByName, &c.CreatedAt, &c.GroupID, &c.Severity, &c.Escalated,
		&c.UpdatedAt,
		&c.ParticipantID, &c.FirstName, &c.LastName, &c.Bib, &c.GroupNumber,
		&c.ContactPhone, &c.EmergencyName, &c.EmergencyPh,
		&c.BloodType, &c.HealthNotes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSOSNotFound
	}
	return &c, err
}

// AdminPatch — แอดมินแก้สถานะเคสด้วยมือ
//
// นี่คือทางออกฉุกเฉินของระบบฉุกเฉิน: เคสที่เจ้าหน้าที่กดปิดผิด เคสที่ปิดไปแล้ว
// แต่เรื่องยังไม่จบ เคสที่ประเมินระดับผิด — ทั้งหมดนี้เกิดตอนงานกำลังวุ่น และ
// ถ้าไม่มีทางแก้ ข้อมูลของทั้งงานจะผิดถาวรโดยไม่มีใครแก้ได้
//
// ทุกฟิลด์เป็น pointer: nil = ไม่แตะ · ไม่ใช่ "ตั้งเป็นค่าว่าง" — แผงส่งมาเฉพาะ
// ช่องที่แอดมินเปลี่ยนจริง การส่งทั้งก้อนแล้วเขียนทับทุกช่องจะลบค่าที่เจ้าหน้าที่
// หน้างานเพิ่งใส่ไประหว่างที่หน้าจอแอดมินเปิดค้างอยู่
//
// COALESCE ไม่ได้ช่วยตรงนี้เพราะ "ล้าง severity" ต้องเขียน NULL ลงไปจริง ๆ ซึ่ง
// แยกจาก "ไม่แตะ severity" ไม่ได้ถ้าใช้ COALESCE — จึงประกอบ SET ทีละชิ้น
func (r *WBWSOSRepository) AdminPatch(ctx context.Context, id int64, p model.SOSAdminPatch, actorID string) (*model.SOSStaffCase, error) {
	sets := []string{"updated_at = now()"}
	args := []any{id}
	// เลขตำแหน่ง $n คิดหลัง append เสมอ — len(args) หลัง append คือเลขที่ถูกพอดี
	// การคิดล่วงหน้าว่า "อันถัดไปคือ $3" พังทันทีที่มีคนแทรกเงื่อนไขใหม่ไว้ตรงกลาง
	set := func(col, cast string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args))+cast)
	}

	if p.Severity != nil {
		if *p.Severity == "" {
			sets = append(sets, "severity = NULL", "severity_at = NULL", "severity_by = NULL")
		} else {
			set("severity", "", *p.Severity)
			sets = append(sets, "severity_at = now()")
			set("severity_by", "::uuid", actorID)
		}
	}
	if p.Escalated != nil {
		set("escalated", "", *p.Escalated)
		if *p.Escalated {
			sets = append(sets, "escalated_at = now()")
			set("escalated_by", "::uuid", actorID)
		} else {
			sets = append(sets, "escalated_at = NULL", "escalated_by = NULL")
		}
	}
	if p.Resolved != nil {
		if *p.Resolved {
			sets = append(sets, "resolved = TRUE", "resolved_at = now()")
			set("resolved_by", "::uuid", actorID)
			reason := "closed_by_admin"
			if p.Reason != nil && strings.TrimSpace(*p.Reason) != "" {
				reason = strings.TrimSpace(*p.Reason)
			}
			set("resolve_reason", "", reason)
		} else {
			// เปิดใหม่ — ล้างร่องรอยการปิดให้หมด ไม่ใช่แค่พลิก boolean
			// ถ้าเหลือ resolved_by/resolved_at ค้างไว้ รายงานจะนับเคสนี้เป็น "ปิดแล้ว"
			// ในบางที่และ "เปิดอยู่" ในบางที่ ตามว่าคิวรีนั้นดูคอลัมน์ไหน
			sets = append(sets, "resolved = FALSE", "resolved_at = NULL",
				"resolved_by = NULL", "resolve_reason = NULL")
		}
	}
	if len(sets) == 1 {
		return r.AdminGet(ctx, id) // ไม่มีอะไรให้แก้ — ไม่ถือเป็น error
	}

	tag, err := r.db.Exec(ctx, `UPDATE sos_event SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// ชน sos_one_open_per_user — คนคนนี้มีเคสอื่นเปิดค้างอยู่แล้ว
		return nil, ErrSOSAlreadyOpen
	}
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrSOSNotFound
	}
	return r.AdminGet(ctx, id)
}
