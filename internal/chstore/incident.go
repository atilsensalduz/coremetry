package chstore

import (
	"context"
	"fmt"
	"time"
)

// Incident is the high-level "something is going wrong" container — a
// declared event the oncall acknowledges, drives to resolution, and
// optionally writes a postmortem against. Multiple Problems / monitor
// flips auto-attach to one Incident when they share the same service +
// severity in a short window, so the oncall has a single page to drive
// the response from.

type Incident struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Severity   string  `json:"severity"`     // info | warning | critical
	Status     string  `json:"status"`       // open | acknowledged | resolved
	Service    string  `json:"service,omitempty"`
	Summary    string  `json:"summary,omitempty"`
	Assignee   string  `json:"assignee,omitempty"`
	Postmortem string  `json:"postmortem,omitempty"`
	StartedAt  int64   `json:"startedAt"`
	AckAt      *int64  `json:"ackAt,omitempty"`
	ResolvedAt *int64  `json:"resolvedAt,omitempty"`
	UpdatedAt  int64   `json:"updatedAt"`
}

type IncidentEvent struct {
	IncidentID string `json:"incidentId"`
	Time       int64  `json:"time"`         // unix ns
	Kind       string `json:"kind"`         // created | ack | resolved | note | problem_attached | problem_resolved
	Actor      string `json:"actor,omitempty"`
	Body       string `json:"body,omitempty"`
	RefID      string `json:"refId,omitempty"`
}

type IncidentFilter struct {
	Status   string
	Service  string
	Severity string
	Limit    int
}

func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var wc whereClause
	if f.Status != "" {
		wc.add("status = ?", f.Status)
	}
	if f.Service != "" {
		wc.add("service = ?", f.Service)
	}
	if f.Severity != "" {
		wc.add("severity = ?", f.Severity)
	}
	rows, err := s.conn.Query(ctx, `
		SELECT id, title, severity, status, service, summary, assignee, postmortem,
		       toUnixTimestamp64Nano(started_at),
		       toUnixTimestamp64Nano(ack_at),
		       toUnixTimestamp64Nano(resolved_at),
		       toUnixTimestamp64Nano(updated_at)
		FROM incidents FINAL `+wc.sql()+`
		ORDER BY started_at DESC
		LIMIT ?`, append(wc.args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var i Incident
		var ack, resolved *int64
		if err := rows.Scan(&i.ID, &i.Title, &i.Severity, &i.Status, &i.Service,
			&i.Summary, &i.Assignee, &i.Postmortem,
			&i.StartedAt, &ack, &resolved, &i.UpdatedAt); err != nil {
			return nil, err
		}
		if ack != nil && *ack > 0 {
			i.AckAt = ack
		}
		if resolved != nil && *resolved > 0 {
			i.ResolvedAt = resolved
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) GetIncident(ctx context.Context, id string) (*Incident, error) {
	rows, err := s.ListIncidents(ctx, IncidentFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	for _, i := range rows {
		if i.ID == id {
			return &i, nil
		}
	}
	return nil, nil
}

// UpsertIncident takes a pointer so auto-generated IDs flow back to the
// caller (matches the monitor/dashboard pattern).
func (s *Store) UpsertIncident(ctx context.Context, i *Incident) error {
	if i.ID == "" {
		i.ID = randHex(8)
	}
	if i.Status == "" {
		i.Status = "open"
	}
	if i.Severity == "" {
		i.Severity = "warning"
	}
	now := time.Now()
	startedAt := time.Unix(0, i.StartedAt).UTC()
	if i.StartedAt == 0 {
		startedAt = now.UTC()
		i.StartedAt = startedAt.UnixNano()
	}
	var ack, resolved *time.Time
	if i.AckAt != nil {
		t := time.Unix(0, *i.AckAt).UTC()
		ack = &t
	}
	if i.ResolvedAt != nil {
		t := time.Unix(0, *i.ResolvedAt).UTC()
		resolved = &t
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO incidents
		(id, title, severity, status, service, summary, assignee, postmortem,
		 started_at, ack_at, resolved_at, updated_at, version)`)
	if err != nil {
		return err
	}
	if err := batch.Append(i.ID, i.Title, i.Severity, i.Status, i.Service,
		i.Summary, i.Assignee, i.Postmortem,
		startedAt, ack, resolved,
		now.UTC(), uint64(now.UnixNano())); err != nil {
		return err
	}
	i.UpdatedAt = now.UnixNano()
	return batch.Send()
}

func (s *Store) AppendIncidentEvent(ctx context.Context, e IncidentEvent) error {
	batch, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO incident_events (incident_id, time, kind, actor, body, ref_id)`)
	if err != nil {
		return err
	}
	t := time.Unix(0, e.Time).UTC()
	if e.Time == 0 {
		t = time.Now().UTC()
	}
	if err := batch.Append(e.IncidentID, t, e.Kind, e.Actor, e.Body, e.RefID); err != nil {
		return err
	}
	return batch.Send()
}

func (s *Store) IncidentTimeline(ctx context.Context, incidentID string) ([]IncidentEvent, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT incident_id, toUnixTimestamp64Nano(time), kind, actor, body, ref_id
		FROM incident_events
		WHERE incident_id = ?
		ORDER BY time ASC`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IncidentEvent{}
	for rows.Next() {
		var e IncidentEvent
		if err := rows.Scan(&e.IncidentID, &e.Time, &e.Kind, &e.Actor, &e.Body, &e.RefID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AttachProblemToIncident either finds an existing OPEN incident
// matching service + severity within `groupingWindow` and attaches the
// problem to it, OR creates a new incident headed by this problem.
// Idempotent — re-attaching a problem refreshes the mapping but
// doesn't duplicate timeline events.
//
// This is the auto-grouping rule: same service + same severity + open
// status + started within the last 30 minutes = one incident. Tunable
// via the constants.
func (s *Store) AttachProblemToIncident(ctx context.Context, p Problem) (*Incident, error) {
	const groupingWindow = 30 * time.Minute

	// Already attached?
	row := s.conn.QueryRow(ctx,
		`SELECT incident_id FROM incident_problems FINAL WHERE problem_id = ? LIMIT 1`, p.ID)
	var existingID string
	if err := row.Scan(&existingID); err == nil && existingID != "" {
		return s.GetIncident(ctx, existingID)
	}

	// Find a matching open incident.
	cutoff := time.Now().Add(-groupingWindow).UTC()
	matchRow := s.conn.QueryRow(ctx, `
		SELECT id FROM incidents FINAL
		WHERE status != 'resolved'
		  AND service  = ?
		  AND severity = ?
		  AND started_at >= ?
		ORDER BY started_at DESC
		LIMIT 1`, p.Service, p.Severity, cutoff)
	var matched string
	_ = matchRow.Scan(&matched)

	var inc Incident
	if matched != "" {
		got, err := s.GetIncident(ctx, matched)
		if err != nil || got == nil {
			matched = ""
		} else {
			inc = *got
		}
	}
	if matched == "" {
		// Create new incident headed by this problem.
		title := p.RuleName
		if p.Service != "" {
			title = p.Service + " — " + p.RuleName
		}
		inc = Incident{
			Title:     title,
			Severity:  p.Severity,
			Status:    "open",
			Service:   p.Service,
			Summary:   p.Description,
			StartedAt: p.StartedAt,
		}
		if err := s.UpsertIncident(ctx, &inc); err != nil {
			return nil, fmt.Errorf("create incident: %w", err)
		}
		_ = s.AppendIncidentEvent(ctx, IncidentEvent{
			IncidentID: inc.ID, Kind: "created", Actor: "system",
			Body: "Auto-created from " + p.RuleName,
		})
	}

	// Bind problem → incident.
	batch, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO incident_problems (problem_id, incident_id, attached_at, version)`)
	if err != nil {
		return &inc, err
	}
	now := time.Now().UTC()
	if err := batch.Append(p.ID, inc.ID, now, uint64(now.UnixNano())); err != nil {
		return &inc, err
	}
	if err := batch.Send(); err != nil {
		return &inc, err
	}

	_ = s.AppendIncidentEvent(ctx, IncidentEvent{
		IncidentID: inc.ID,
		Kind:       "problem_attached",
		Actor:      "system",
		Body:       p.RuleName,
		RefID:      p.ID,
	})
	return &inc, nil
}

// IncidentProblems lists all problem ids attached to an incident.
func (s *Store) IncidentProblems(ctx context.Context, incidentID string) ([]string, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT problem_id FROM incident_problems FINAL WHERE incident_id = ?`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
