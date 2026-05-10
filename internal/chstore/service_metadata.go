package chstore

import (
	"context"
	"strings"
	"time"
)

// ServiceMetadata is operator-curated context for a single
// service: owner team, oncall channel, runbook URL, repo, and
// a free-text description. Joins to the spans table by
// service_name. Lives in a tiny ReplacingMergeTree separate
// from the spans hot path because the data doesn't fit a
// span resource attribute (it's per-team-decided, not per-
// span-emitted) and the row count is bounded by service
// count not span count.
//
// All fields except `service` are optional; an empty row
// surfaces as "no metadata yet" on the frontend with an edit
// CTA so the catalog grows organically.
type ServiceMetadata struct {
	Service      string `json:"service"`
	OwnerTeam    string `json:"ownerTeam,omitempty"`
	// SRETeam is the platform / reliability team that owns
	// the operational health of the service — typically
	// distinct from the product owner team. Surfaces as a
	// second chip on the catalog pill so the oncall who
	// inherits the service knows who to escalate to for
	// infra issues vs feature regressions.
	SRETeam      string `json:"sreTeam,omitempty"`
	Description  string `json:"description,omitempty"`
	Repository   string `json:"repository,omitempty"`
	RunbookURL   string `json:"runbookUrl,omitempty"`
	OncallURL    string `json:"oncallUrl,omitempty"`
	// ChatChannel — Zoom Chat / Mattermost / Slack channel
	// for the team. Renamed from slack_channel because the
	// catalog target cluster runs on Zoom Chat; the legacy
	// slack_channel column still backfills here on read so
	// pre-rename rows keep showing.
	ChatChannel  string `json:"chatChannel,omitempty"`
	UpdatedAt    int64  `json:"updatedAt"` // unix nanoseconds
}

// GetServiceMetadata returns the catalog row for one service.
// Missing rows return nil, nil — the page handles the empty
// state inline (no special "404" UI needed).
//
// Read-time fallback: chat_channel is the new column; if a
// pre-rename row only populated slack_channel we surface that
// value so legacy curation doesn't disappear from the UI.
func (s *Store) GetServiceMetadata(ctx context.Context, service string) (*ServiceMetadata, error) {
	if service == "" {
		return nil, nil
	}
	row := s.conn.QueryRow(ctx, `
		SELECT service, owner_team, sre_team, description, repository,
		       runbook_url, oncall_url, chat_channel, slack_channel,
		       toUnixTimestamp64Nano(updated_at)
		FROM service_metadata FINAL
		WHERE service = ?
		LIMIT 1`, service)
	var m ServiceMetadata
	var legacySlack string
	if err := row.Scan(&m.Service, &m.OwnerTeam, &m.SRETeam, &m.Description, &m.Repository,
		&m.RunbookURL, &m.OncallURL, &m.ChatChannel, &legacySlack, &m.UpdatedAt); err != nil {
		// "no rows" → not yet curated; same handling pattern
		// the rest of chstore uses.
		return nil, nil
	}
	if m.ChatChannel == "" && legacySlack != "" {
		m.ChatChannel = legacySlack
	}
	return &m, nil
}

// ListServiceMetadata returns every catalog row in one shot —
// used by the /services list to render the owner-team chip on
// every row without N round-trips. Cheap because the table is
// at most a few thousand rows.
func (s *Store) ListServiceMetadata(ctx context.Context) (map[string]ServiceMetadata, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT service, owner_team, sre_team, description, repository,
		       runbook_url, oncall_url, chat_channel, slack_channel,
		       toUnixTimestamp64Nano(updated_at)
		FROM service_metadata FINAL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ServiceMetadata, 64)
	for rows.Next() {
		var m ServiceMetadata
		var legacySlack string
		if err := rows.Scan(&m.Service, &m.OwnerTeam, &m.SRETeam, &m.Description, &m.Repository,
			&m.RunbookURL, &m.OncallURL, &m.ChatChannel, &legacySlack, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if m.ChatChannel == "" && legacySlack != "" {
			m.ChatChannel = legacySlack
		}
		out[m.Service] = m
	}
	return out, rows.Err()
}

// UpsertServiceMetadata writes a catalog row. Last-write-wins
// via the ReplacingMergeTree's version column; UpdatedAt is
// always stamped to now() so the operator sees fresh edit
// times in the list. Empty `service` is a no-op (you can't
// curate "no service").
//
// Writes only the new chat_channel column. The legacy
// slack_channel column is left as-is so an upgrade-then-
// downgrade still surfaces the original value; the next edit
// after upgrade migrates the value into chat_channel via the
// read-time fallback.
func (s *Store) UpsertServiceMetadata(ctx context.Context, m ServiceMetadata) error {
	m.Service = strings.TrimSpace(m.Service)
	if m.Service == "" {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO service_metadata
		(service, owner_team, sre_team, description, repository,
		 runbook_url, oncall_url, chat_channel, updated_at, version)`)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := batch.Append(m.Service, m.OwnerTeam, m.SRETeam, m.Description, m.Repository,
		m.RunbookURL, m.OncallURL, m.ChatChannel,
		now.UTC(), uint64(now.UnixNano())); err != nil {
		return err
	}
	return batch.Send()
}
