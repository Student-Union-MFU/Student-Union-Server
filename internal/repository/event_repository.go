// Package repository is for uh sql queries? i think
package repository

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"su-server/internal/model"
)

type EventRepository struct {
	db *pgxpool.Pool
}

func NewEventRepository(db *pgxpool.Pool) *EventRepository {
	return &EventRepository{db: db}
}

func (r *EventRepository) GetAllEvents(ctx context.Context) ([]model.Event, error) {
	rows, err := r.db.Query(ctx, "SELECT id, title, content, location, date, time, link, created_at FROM events")
	if err != nil {
		return nil, err
	}

	var events []model.Event
	for rows.Next() {
		var event model.Event
		err := rows.Scan(
			&event.ID,
			&event.Title,
			&event.Content,
			&event.Location,
			&event.Date,
			&event.Time,
			&event.Link,
			&event.CreatedAt,
		)
		if err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	rows.Close() // close outer rows before any more queries

	// now fetch images for each event
	for i, event := range events {
		imgRows, err := r.db.Query(ctx, "SELECT id, event_id, url, position FROM event_images WHERE event_id = $1 ORDER BY position", event.ID)
		if err != nil {
			return nil, err
		}
		for imgRows.Next() {
			var img model.EventImage
			if err := imgRows.Scan(&img.ID, &img.EventID, &img.URL, &img.Position); err != nil {
				imgRows.Close()
				return nil, err
			}
			events[i].Images = append(events[i].Images, img)
		}
		imgRows.Close()
	}

	return events, nil
}

func (r *EventRepository) GetOneEvent(id int, ctx context.Context) (*model.Event, error) {
	var event model.Event
	err := r.db.QueryRow(ctx,
		"SELECT id, title, content, location, date, time, link, created_at FROM events WHERE id = $1", id,
	).Scan(
		&event.ID,
		&event.Title,
		&event.Content,
		&event.Location,
		&event.Date,
		&event.Time,
		&event.Link,
		&event.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	imgRows, err := r.db.Query(ctx,
		"SELECT id, event_id, url, position FROM event_images WHERE event_id = $1 ORDER BY position", id,
	)
	if err != nil {
		return nil, err
	}
	defer imgRows.Close()

	for imgRows.Next() {
		var img model.EventImage
		if err := imgRows.Scan(&img.ID, &img.EventID, &img.URL, &img.Position); err != nil {
			return nil, err
		}
		event.Images = append(event.Images, img)
	}

	return &event, nil
}

func (r *EventRepository) GetAllEvent(ctx context.Context) ([]model.Event, error) {
	var events []model.Event
	rows, err := r.db.Query(ctx, "SELECT * FROM events")

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	for rows.Next() {
		var event model.Event
		err := rows.Scan(
			&event.ID,
			&event.Title,
			&event.Content,
			&event.Date,
			&event.CreatedAt,
			&event.Images,
			&event.Link,
			&event.Location,
			&event.Time,
		)
		if err != nil {
			return []model.Event{}, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (r *EventRepository) InsertOneEvent(ctx context.Context, event model.Event) (bool, error) {

	var eventID int

	err := r.db.QueryRow(ctx,
		"INSERT INTO events (title, content, location, date, time, link) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
		event.Title, event.Content, event.Location, event.Date, event.Time, event.Link,
	).Scan(&eventID)

	if err != nil {
		return false, err
	}

	for _, img := range event.Images {
		_, err := r.db.Exec(ctx,
			"INSERT INTO event_images (event_id, url, position) VALUES ($1, $2, $3)",
			eventID, img.URL, img.Position,
		)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

func (r *EventRepository) InsertMultipleEvents(ctx context.Context, events []model.Event) (bool, error) {
	for _, event := range events {
		_, err := r.db.Exec(ctx,
			"INSERT INTO events (title, content, location, date, time, link) VALUES ($1, $2, $3, $4, $5, $6)",
			event.Title, event.Content, event.Location, event.Date, event.Time, event.Link,
		)
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func (r *EventRepository) UpdateOneEvent(ctx context.Context, id int, event model.Event) (*model.Event, error) {
	var result model.Event
	err := r.db.QueryRow(ctx,
		`UPDATE events
		 SET title    = $1,
		     content  = $2,
		     location = $3,
		     date     = $4,
		     time     = $5,
		     link     = $6
		 WHERE id = $7
		 RETURNING id, title, content, location, date, time, link, created_at`,
		event.Title,
		event.Content,
		event.Location,
		event.Date,
		event.Time,
		event.Link,
		id,
	).Scan(
		&result.ID,
		&result.Title,
		&result.Content,
		&result.Location,
		&result.Date,
		&result.Time,
		&result.Link,
		&result.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	imgRows, err := r.db.Query(ctx,
		"SELECT id, event_id, url, position FROM event_images WHERE event_id = $1 ORDER BY position", id,
	)
	if err != nil {
		return nil, err
	}
	defer imgRows.Close()

	for imgRows.Next() {
		var img model.EventImage
		if err := imgRows.Scan(&img.ID, &img.EventID, &img.URL, &img.Position); err != nil {
			return nil, err
		}
		result.Images = append(result.Images, img)
	}

	return &result, nil
}

func (r *EventRepository) DeletetOneEvent(id int, ctx context.Context) (bool, error) {
	_, err := r.db.Exec(ctx,
		"DELETE FROM events WHERE id = $1", id)
	if err != nil {
		return false, err
	}
	return true, nil
}
