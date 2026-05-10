package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ── system_settings ─────────────────────────────────────────────────────────
//
// Key/value store for global configuration that has to outlive a process.
// SMTP credentials live here today; future global toggles (signup
// allowed?, default retention overrides…) can reuse it.

// GetSetting returns the JSON-encoded value for key, or nil if missing.
func (s *Store) GetSetting(ctx context.Context, key string) ([]byte, error) {
	row := s.conn.QueryRow(ctx, `
		SELECT value FROM system_settings FINAL WHERE key = ? LIMIT 1`, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return []byte(v), nil
}

// PutSetting upserts the JSON-encoded value at key.
func (s *Store) PutSetting(ctx context.Context, key string, value []byte) error {
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO system_settings (key, value)")
	if err != nil {
		return fmt.Errorf("prepare settings: %w", err)
	}
	if err := batch.Append(key, string(value)); err != nil {
		return fmt.Errorf("append setting: %w", err)
	}
	return batch.Send()
}

// ── notification_channels ───────────────────────────────────────────────────

type NotificationChannel struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`        // email | slack | webhook
	Config      json.RawMessage `json:"config"`      // type-specific
	Enabled     bool            `json:"enabled"`
	MinSeverity string          `json:"minSeverity"` // info | warning | critical
	// MatchRules — routing predicates. Empty / zero-value
	// fields mean "match anything" so the default channel
	// stays a catch-all. Populated arrays AND together: a
	// channel only fires when its services / sreTeams /
	// ownerTeams ALL match the problem's service catalog.
	MatchRules  ChannelMatchRules `json:"matchRules,omitempty"`
	CreatedAt   int64           `json:"createdAt"`   // unix ns
}

// ChannelMatchRules — small predicate set that gates
// delivery per channel. Each list is "OR within, AND between
// lists":
//   • services    = []string of literal service names
//   • sreTeams    = []string of catalog SRE team names
//   • ownerTeams  = []string of catalog product owner team names
type ChannelMatchRules struct {
	Services   []string `json:"services,omitempty"`
	SRETeams   []string `json:"sreTeams,omitempty"`
	OwnerTeams []string `json:"ownerTeams,omitempty"`
}

// Matches — true when the channel should fire for `service`
// given its catalog metadata. Empty / zero-value rules
// mean catch-all (always true).
func (m ChannelMatchRules) Matches(service string, md *ServiceMetadata) bool {
	if len(m.Services) > 0 {
		hit := false
		for _, s := range m.Services {
			if s == service {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(m.SRETeams) > 0 {
		if md == nil {
			return false
		}
		hit := false
		for _, t := range m.SRETeams {
			if t == md.SRETeam {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(m.OwnerTeams) > 0 {
		if md == nil {
			return false
		}
		hit := false
		for _, t := range m.OwnerTeams {
			if t == md.OwnerTeam {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (s *Store) ListChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT id, name, type, config, enabled, min_severity,
		       match_rules,
		       toUnixTimestamp64Nano(created_at)
		FROM notification_channels FINAL
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		var c NotificationChannel
		var config, matchRules string
		var enabled uint8
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &config, &enabled, &c.MinSeverity,
			&matchRules, &c.CreatedAt); err != nil {
			return nil, err
		}
		if config == "" {
			config = "{}"
		}
		c.Config = json.RawMessage(config)
		c.Enabled = enabled != 0
		// Match rules are stored as a JSON blob in the column;
		// errors (malformed legacy data) collapse to the
		// empty / catch-all value rather than dropping the
		// whole channel.
		if matchRules != "" {
			_ = json.Unmarshal([]byte(matchRules), &c.MatchRules)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EnabledChannelsForSeverity is what the notifier calls when a Problem
// opens. Returns only enabled channels whose min_severity ≤ the problem's
// severity (so a "critical" problem fires every channel; "info" fires
// only the ones explicitly subscribed at info level).
func (s *Store) EnabledChannelsForSeverity(ctx context.Context, severity string) ([]NotificationChannel, error) {
	all, err := s.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	threshold := severityRank(severity)
	out := make([]NotificationChannel, 0, len(all))
	for _, c := range all {
		if !c.Enabled {
			continue
		}
		if severityRank(c.MinSeverity) > threshold {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) GetChannel(ctx context.Context, id string) (*NotificationChannel, error) {
	row := s.conn.QueryRow(ctx, `
		SELECT id, name, type, config, enabled, min_severity,
		       match_rules,
		       toUnixTimestamp64Nano(created_at)
		FROM notification_channels FINAL
		WHERE id = ? LIMIT 1`, id)
	var c NotificationChannel
	var config, matchRules string
	var enabled uint8
	if err := row.Scan(&c.ID, &c.Name, &c.Type, &config, &enabled, &c.MinSeverity,
		&matchRules, &c.CreatedAt); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	if config == "" {
		config = "{}"
	}
	c.Config = json.RawMessage(config)
	c.Enabled = enabled != 0
	if matchRules != "" {
		_ = json.Unmarshal([]byte(matchRules), &c.MatchRules)
	}
	return &c, nil
}

func (s *Store) UpsertChannel(ctx context.Context, c NotificationChannel) error {
	if c.ID == "" {
		return fmt.Errorf("channel id required")
	}
	if c.MinSeverity == "" {
		c.MinSeverity = "warning"
	}
	if len(c.Config) == 0 {
		c.Config = json.RawMessage("{}")
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().UnixNano()
	}
	// Marshal match rules into the column. Always populate
	// the column so the read path doesn't have to handle a
	// "missing argument" form for the legacy rows that pre-
	// date the column — the migration ALTER + the always-
	// populated insert keep the shape stable.
	mr, err := json.Marshal(c.MatchRules)
	if err != nil {
		return fmt.Errorf("marshal match rules: %w", err)
	}
	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO notification_channels (id, name, type, config, enabled, min_severity, match_rules)")
	if err != nil {
		return fmt.Errorf("prepare channels: %w", err)
	}
	var en uint8
	if c.Enabled {
		en = 1
	}
	if err := batch.Append(c.ID, c.Name, c.Type, string(c.Config), en, c.MinSeverity, string(mr)); err != nil {
		return fmt.Errorf("append channel: %w", err)
	}
	return batch.Send()
}

func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	return s.conn.Exec(ctx, `ALTER TABLE notification_channels DELETE WHERE id = ?`, id)
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 2 // unknown → treat as warning
}
