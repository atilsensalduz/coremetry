// Package ldap is the enterprise auth provider — connects to a
// corporate LDAP/AD directory, authenticates users with their domain
// credentials, and resolves their group memberships into Coremetry
// roles via an admin-configurable mapping.
//
// Designed for enterprise-style on-prem deployments:
//   - LDAPS (port 636) is the default; StartTLS (389→TLS upgrade) is
//     supported for legacy AD setups.
//   - Custom CA paste field for internal CAs; SkipVerify toggle as
//     last-resort escape hatch for self-signed certs.
//   - Group→role mapping is the primary provisioning path. Pre-
//     provisioned users (admin pinned a row) override the group map.
//   - Bind password is stored in plaintext in system_settings — that
//     was an explicit deployment-time decision; an env-keyed encrypt
//     path can be bolted on later if needed.
package ldap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// Config is the persisted LDAP connection + mapping definition.
//
// Defaults are filled in by Normalize() so the config struct can be
// built from a half-empty PUT body and still produce sensible probes.
type Config struct {
	Enabled bool `json:"enabled"`

	// Connection
	Host       string `json:"host"`
	Port       int    `json:"port"`
	UseTLS     bool   `json:"useTLS"`     // direct ldaps:// (default port 636)
	StartTLS   bool   `json:"startTLS"`   // upgrade plain → TLS on 389
	SkipVerify bool   `json:"skipVerify"`
	CACert     string `json:"caCert"` // PEM bundle for internal CA

	// Service account used to look up users / groups.
	BindDN       string `json:"bindDN"`
	BindPassword string `json:"bindPassword"`

	// Search
	BaseDN           string `json:"baseDN"`
	UserSearchFilter string `json:"userSearchFilter"` // {{username}} placeholder
	UserAttribute    string `json:"userAttribute"`    // sAMAccountName | uid | mail
	EmailAttribute   string `json:"emailAttribute"`
	DisplayAttribute string `json:"displayAttribute"`

	// Group lookup
	GroupSearchBase string `json:"groupSearchBase"`
	GroupFilter     string `json:"groupFilter"` // {{userDN}} placeholder

	// Role assignment
	DefaultRole  string             `json:"defaultRole"`  // role for users without group match
	GroupRoleMap []GroupRoleMapping `json:"groupRoleMap"` // first match wins (admin > editor > viewer)
}

type GroupRoleMapping struct {
	Group string `json:"group"` // group DN (preferred) or CN — case-insensitive substring match
	Role  string `json:"role"`  // admin | editor | viewer
}

// Normalize fills in Active-Directory-friendly defaults so half-filled
// configs produce a working probe. Mutates in place.
func (c *Config) Normalize() {
	if c.Port == 0 {
		if c.UseTLS {
			c.Port = 636
		} else {
			c.Port = 389
		}
	}
	if c.UserSearchFilter == "" {
		c.UserSearchFilter = "(sAMAccountName={{username}})"
	}
	if c.UserAttribute == "" {
		c.UserAttribute = "sAMAccountName"
	}
	if c.EmailAttribute == "" {
		c.EmailAttribute = "mail"
	}
	if c.DisplayAttribute == "" {
		c.DisplayAttribute = "displayName"
	}
	if c.GroupFilter == "" {
		c.GroupFilter = "(member={{userDN}})"
	}
	if c.DefaultRole == "" {
		c.DefaultRole = "viewer"
	}
}

// Sanitize returns a copy with the bind password cleared — used for
// API responses so the secret never round-trips back to the UI.
func (c *Config) Sanitize() Config {
	out := *c
	out.BindPassword = ""
	if c.BindPassword != "" {
		// One-bit indicator that a password is set, mapped client-side
		// to "leave empty to keep current". Empty would mean "no pwd".
		out.BindPassword = "__SET__"
	}
	out.GroupRoleMap = append([]GroupRoleMapping(nil), c.GroupRoleMap...)
	return out
}

// LDAPUser is the lightweight projection of a directory entry that
// the UI consumes (search results + provisioning picker).
type LDAPUser struct {
	DN          string   `json:"dn"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	DisplayName string   `json:"displayName"`
	Groups      []string `json:"groups,omitempty"`
}

// AuthResult bundles the authenticated user + the role we resolved
// from their group memberships.
type AuthResult struct {
	User LDAPUser
	Role string
}

// ── Service ─────────────────────────────────────────────────────────────────

// Service holds the live config; mutates safely under RWMutex so the
// admin Settings PUT can swap config while a login is in flight.
type Service struct {
	mu  sync.RWMutex
	cfg Config
}

func New() *Service { return &Service{} }

func (s *Service) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Enabled && s.cfg.Host != ""
}

// Snapshot returns a sanitized copy (no plain bind password).
func (s *Service) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Sanitize()
}

// rawConfig returns a full copy including the bind password — for
// internal use only (connection establishment).
func (s *Service) rawConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.GroupRoleMap = append([]GroupRoleMapping(nil), s.cfg.GroupRoleMap...)
	return c
}

// Configure swaps the live config. If incoming.BindPassword == "" but
// a password is already saved, the old one is preserved (matches the
// "leave empty to keep current" UX).
func (s *Service) Configure(incoming Config) {
	incoming.Normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	if incoming.BindPassword == "" && s.cfg.BindPassword != "" {
		incoming.BindPassword = s.cfg.BindPassword
	}
	s.cfg = incoming
}

// ── Persistence ─────────────────────────────────────────────────────────────

const settingsKey = "ldap"

type SettingsStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

func (s *Service) LoadPersisted(ctx context.Context, store SettingsStore) error {
	raw, err := store.GetSetting(ctx, settingsKey)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	c.Normalize()
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	return nil
}

func (s *Service) SavePersisted(ctx context.Context, store SettingsStore, c Config) error {
	c.Normalize()
	// Preserve existing bind password when caller submits empty.
	s.mu.Lock()
	if c.BindPassword == "" && s.cfg.BindPassword != "" {
		c.BindPassword = s.cfg.BindPassword
	}
	s.cfg = c
	s.mu.Unlock()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return store.PutSetting(ctx, settingsKey, raw)
}

// ── Connection ──────────────────────────────────────────────────────────────

// dial opens a connection using the saved config (or a transient one
// for TestConnection). Caller is responsible for Close().
func dial(c Config) (*goldap.Conn, error) {
	c.Normalize()
	if c.Host == "" {
		return nil, errors.New("ldap host not configured")
	}
	addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	tlsCfg := &tls.Config{ServerName: c.Host, InsecureSkipVerify: c.SkipVerify}
	if c.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(c.CACert)) {
			return nil, errors.New("ldap: failed to parse CA cert (expecting PEM)")
		}
		tlsCfg.RootCAs = pool
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var (
		conn *goldap.Conn
		err  error
	)
	switch {
	case c.UseTLS:
		conn, err = goldap.DialURL("ldaps://"+addr,
			goldap.DialWithTLSConfig(tlsCfg),
			goldap.DialWithDialer(dialer))
	default:
		conn, err = goldap.DialURL("ldap://"+addr,
			goldap.DialWithDialer(dialer))
	}
	if err != nil {
		return nil, fmt.Errorf("ldap dial %s: %w", addr, err)
	}
	conn.SetTimeout(10 * time.Second)
	if c.StartTLS && !c.UseTLS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("ldap StartTLS: %w", err)
		}
	}
	return conn, nil
}

// bindAdmin opens a connection and binds with the configured service
// account. Returned conn must be closed by the caller.
func bindAdmin(c Config) (*goldap.Conn, error) {
	conn, err := dial(c)
	if err != nil {
		return nil, err
	}
	if c.BindDN != "" {
		if err := conn.Bind(c.BindDN, c.BindPassword); err != nil {
			conn.Close()
			return nil, fmt.Errorf("ldap bind %q: %w", c.BindDN, err)
		}
	}
	return conn, nil
}

// ── Operations ──────────────────────────────────────────────────────────────

// TestConnection establishes + service-binds + closes. Returns nil on
// success so the UI's "Test connection" button can flip green.
func (s *Service) TestConnection(ctx context.Context, override *Config) error {
	cfg := s.rawConfig()
	if override != nil {
		// Caller is testing a draft config the admin hasn't saved yet.
		// Inherit the saved password when override leaves it empty so
		// the test can be re-run against the existing creds.
		c := *override
		if c.BindPassword == "" {
			c.BindPassword = cfg.BindPassword
		}
		cfg = c
	}
	conn, err := bindAdmin(cfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	return nil
}

// findUser looks up a directory entry by username (resolving the
// configured filter template). Returns (nil, nil) for "no such user".
func findUser(conn *goldap.Conn, c Config, username string) (*LDAPUser, error) {
	filter := strings.ReplaceAll(c.UserSearchFilter, "{{username}}", goldap.EscapeFilter(username))
	req := goldap.NewSearchRequest(
		c.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		2, 5, false,
		filter,
		[]string{"dn", c.UserAttribute, c.EmailAttribute, c.DisplayAttribute, "memberOf"},
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("user search: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, nil
	}
	if len(res.Entries) > 1 {
		return nil, fmt.Errorf("user search returned %d entries (filter too loose?)", len(res.Entries))
	}
	e := res.Entries[0]
	groups := e.GetAttributeValues("memberOf")
	return &LDAPUser{
		DN:          e.DN,
		Username:    firstNonEmpty(e.GetAttributeValue(c.UserAttribute), username),
		Email:       e.GetAttributeValue(c.EmailAttribute),
		DisplayName: e.GetAttributeValue(c.DisplayAttribute),
		Groups:      groups,
	}, nil
}

// Search looks up users matching `query` (substring on username,
// email or displayName). Used by the admin "pick a user to provision"
// flow. Returns at most `limit` entries.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]LDAPUser, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	c := s.rawConfig()
	conn, err := bindAdmin(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	q := goldap.EscapeFilter(query)
	filter := fmt.Sprintf("(|(%s=*%s*)(%s=*%s*)(%s=*%s*))",
		c.UserAttribute, q, c.EmailAttribute, q, c.DisplayAttribute, q)
	if query == "" {
		filter = fmt.Sprintf("(%s=*)", c.UserAttribute)
	}
	req := goldap.NewSearchRequest(
		c.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		limit, 10, false,
		filter,
		[]string{"dn", c.UserAttribute, c.EmailAttribute, c.DisplayAttribute},
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	out := make([]LDAPUser, 0, len(res.Entries))
	for _, e := range res.Entries {
		out = append(out, LDAPUser{
			DN:          e.DN,
			Username:    e.GetAttributeValue(c.UserAttribute),
			Email:       e.GetAttributeValue(c.EmailAttribute),
			DisplayName: e.GetAttributeValue(c.DisplayAttribute),
		})
	}
	return out, nil
}

// Authenticate runs the standard "search-then-bind" auth pattern:
//   1. Service-bind (admin lookup credentials).
//   2. Find the user by username (or email — `username` may be either,
//      the configured UserSearchFilter handles it).
//   3. Re-bind with the user's DN + entered password — that's the
//      actual credential check.
//   4. Resolve groups → role via the configured GroupRoleMap; fall
//      back to DefaultRole.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*AuthResult, error) {
	if password == "" {
		return nil, errors.New("password required")
	}
	c := s.rawConfig()
	if !c.Enabled {
		return nil, errors.New("ldap disabled")
	}
	conn, err := bindAdmin(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	user, err := findUser(conn, c, username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, errors.New("user not found in directory")
	}
	if err := conn.Bind(user.DN, password); err != nil {
		return nil, errors.New("invalid credentials")
	}

	// Group lookup — separate search if the user entry didn't ship
	// memberOf (some directories don't populate it). Also folds the
	// memberOf list we already have so we get the full picture.
	groups := append([]string(nil), user.Groups...)
	if c.GroupSearchBase != "" {
		grpFilter := strings.ReplaceAll(c.GroupFilter, "{{userDN}}", goldap.EscapeFilter(user.DN))
		grpReq := goldap.NewSearchRequest(
			c.GroupSearchBase, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
			0, 5, false,
			grpFilter,
			[]string{"dn", "cn"},
			nil,
		)
		grpRes, err := conn.Search(grpReq)
		if err == nil {
			for _, e := range grpRes.Entries {
				groups = append(groups, e.DN)
			}
		}
	}
	user.Groups = groups
	role := mapRole(groups, c.GroupRoleMap, c.DefaultRole)
	return &AuthResult{User: *user, Role: role}, nil
}

// mapRole picks the highest-privilege role any of the user's groups
// matches. Match is case-insensitive substring — works for both DN
// ("CN=Coremetry-Admins,OU=Groups,DC=corp,DC=example") and bare CN
// ("Coremetry-Admins") values in the mapping config.
func mapRole(userGroups []string, mappings []GroupRoleMapping, fallback string) string {
	rank := func(role string) int {
		switch role {
		case "admin":
			return 3
		case "editor":
			return 2
		case "viewer":
			return 1
		}
		return 0
	}
	best := ""
	for _, m := range mappings {
		needle := strings.ToLower(strings.TrimSpace(m.Group))
		if needle == "" {
			continue
		}
		for _, g := range userGroups {
			if strings.Contains(strings.ToLower(g), needle) {
				if rank(m.Role) > rank(best) {
					best = m.Role
				}
				break
			}
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
