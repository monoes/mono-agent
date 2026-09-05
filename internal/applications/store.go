// internal/applications/store.go
package applications

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Store provides CRUD operations for applications, backed by the tables
// created by data/migrations/031_applications.sql.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store backed by the given database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func nullFloat(f *float64) interface{} {
	if f == nil {
		return nil
	}
	return *f
}

func nullBool(b *bool) interface{} {
	if b == nil {
		return nil
	}
	return *b
}

// validateAndDefault checks kind/detail consistency and required per-kind
// fields, defaulting ProfileID. It does not touch the database.
func validateAndDefault(app *Application) error {
	if app.ProfileID == "" {
		app.ProfileID = "default"
	}
	switch app.Kind {
	case KindJob:
		if app.Tender != nil {
			return fmt.Errorf("%w: kind is %q but tender details were supplied", ErrInvalidInput, KindJob)
		}
		if app.Job == nil {
			return fmt.Errorf("%w: kind %q requires job details", ErrInvalidInput, KindJob)
		}
		if app.Job.Company == "" {
			return fmt.Errorf("%w: job.company is required", ErrInvalidInput)
		}
		if app.Job.URL == "" {
			return fmt.Errorf("%w: job.url is required", ErrInvalidInput)
		}
	case KindTender:
		if app.Job != nil {
			return fmt.Errorf("%w: kind is %q but job details were supplied", ErrInvalidInput, KindTender)
		}
		if app.Tender == nil {
			return fmt.Errorf("%w: kind %q requires tender details", ErrInvalidInput, KindTender)
		}
		if app.Tender.IssuingOrg == "" {
			return fmt.Errorf("%w: tender.issuing_org is required", ErrInvalidInput)
		}
		if app.Tender.URL == "" {
			return fmt.Errorf("%w: tender.url is required", ErrInvalidInput)
		}
		if app.Tender.SubmissionDeadline == "" {
			return fmt.Errorf("%w: tender.submission_deadline is required", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q (must be %q or %q)", ErrInvalidInput, app.Kind, KindJob, KindTender)
	}
	return nil
}

// Create inserts a new application plus its matching detail row and initial
// status-log entry, all in one transaction. On success app.ID, app.Status,
// app.CreatedAt, and app.UpdatedAt are populated.
func (s *Store) Create(ctx context.Context, app *Application) error {
	if err := validateAndDefault(app); err != nil {
		return err
	}

	app.ID = uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	app.Status = StatusPending
	app.CreatedAt = now
	app.UpdatedAt = now

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("applications.Create: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO applications (id, profile_id, kind, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		app.ID, app.ProfileID, string(app.Kind), string(app.Status), app.CreatedAt, app.UpdatedAt,
	); err != nil {
		return fmt.Errorf("applications.Create: insert application: %w", err)
	}

	switch app.Kind {
	case KindJob:
		j := app.Job
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO job_details (application_id, company, url, location, description,
			        compensation_min, compensation_max, currency, job_type, is_remote, source, posted_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			app.ID, j.Company, j.URL, j.Location, j.Description,
			nullFloat(j.CompensationMin), nullFloat(j.CompensationMax), j.Currency, j.JobType,
			nullBool(j.IsRemote), j.Source, j.PostedAt,
		); err != nil {
			return fmt.Errorf("applications.Create: insert job_details: %w", err)
		}
	case KindTender:
		td := app.Tender
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tender_details (application_id, issuing_org, url, description,
			        submission_deadline, estimated_value, currency, required_certifications,
			        bid_documents_required, source, published_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			app.ID, td.IssuingOrg, td.URL, td.Description, td.SubmissionDeadline,
			nullFloat(td.EstimatedValue), td.Currency, td.RequiredCertifications,
			td.BidDocumentsRequired, td.Source, td.PublishedAt,
		); err != nil {
			return fmt.Errorf("applications.Create: insert tender_details: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO application_status_log (id, application_id, from_status, to_status, actor, note, created_at)
		 VALUES (?, ?, NULL, ?, ?, ?, ?)`,
		uuid.NewString(), app.ID, string(StatusPending), string(ActorUser), "created", now,
	); err != nil {
		return fmt.Errorf("applications.Create: insert status log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("applications.Create: commit: %w", err)
	}
	return nil
}

// allowedTransitions is the closed transition graph. rejected/cancelled
// have no outgoing edges (terminal).
var allowedTransitions = map[Status][]Status{
	StatusPending: {StatusApplied, StatusCancelled},
	StatusApplied: {StatusRejected, StatusCancelled},
}

func isAllowedTransition(from, to Status) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// SetStatus transitions an application's status, validating against the
// allowed transition graph and appending exactly one status_log row, both
// in a single transaction. Returns ErrNotFound if id/profileID doesn't
// match a row, ErrInvalidTransition if the edge isn't allowed.
func (s *Store) SetStatus(ctx context.Context, profileID, id string, to Status, actor Actor, note string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("applications.SetStatus: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var current string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM applications WHERE id = ? AND profile_id = ?`, id, profileID,
	).Scan(&current)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: application %q", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("applications.SetStatus: lookup: %w", err)
	}

	from := Status(current)
	if !isAllowedTransition(from, to) {
		return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE applications SET status = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
		string(to), now, id, profileID,
	); err != nil {
		return fmt.Errorf("applications.SetStatus: update: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO application_status_log (id, application_id, from_status, to_status, actor, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), id, string(from), string(to), string(actor), note, now,
	); err != nil {
		return fmt.Errorf("applications.SetStatus: insert status log: %w", err)
	}
	return tx.Commit()
}

// StatusLog returns id's full transition history, oldest first. Returns
// ErrNotFound if id/profileID doesn't match a row.
func (s *Store) StatusLog(ctx context.Context, profileID, id string) ([]StatusLogEntry, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM applications WHERE id = ? AND profile_id = ?`, id, profileID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("applications.StatusLog: %w", err)
	}
	if exists == 0 {
		return nil, fmt.Errorf("%w: application %q", ErrNotFound, id)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(from_status, ''), to_status, actor, note, created_at
		 FROM application_status_log WHERE application_id = ? ORDER BY created_at ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("applications.StatusLog: query: %w", err)
	}
	defer rows.Close()

	var out []StatusLogEntry
	for rows.Next() {
		var e StatusLogEntry
		var toStatus, actor string
		if err := rows.Scan(&e.ID, &e.FromStatus, &toStatus, &actor, &e.Note, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("applications.StatusLog: scan: %w", err)
		}
		e.ToStatus = Status(toStatus)
		e.Actor = Actor(actor)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("applications.StatusLog: rows: %w", err)
	}
	return out, nil
}
