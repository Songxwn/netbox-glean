// Package store persists settings and targets as JSON files.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	settingsFile  = "settings.json"
	targetsFile   = "targets.json"
	ipTargetsFile = "ip_targets.json"
)

// Store is a small thread-safe JSON backed store. Credentials are stored in
// plain text with file mode 0600; protect the data directory accordingly.
type Store struct {
	mu              sync.RWMutex
	dir             string
	settings        model.Settings
	targets         []model.Target
	ipTargets       []model.IPTarget
	settingsExisted bool
}

// Open creates the data directory if needed and loads existing state.
func Open(dir string) (*Store, error) {
	if dir == "" {
		dir = "data"
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	s := &Store{dir: dir, settings: model.DefaultSettings()}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	if err := s.loadIPTargets(); err != nil {
		return nil, err
	}
	legacy, err := s.loadTargets()
	if err != nil {
		return nil, err
	}
	if len(legacy) > 0 {
		if err := s.migrateIPTargets(legacy); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) loadSettings() error {
	data, err := os.ReadFile(filepath.Join(s.dir, settingsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read settings: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	s.settingsExisted = true
	var settings model.Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse settings.json: %w", err)
	}
	// sync_cables was added after the first release. Existing installations
	// have no key for it, which would otherwise unmarshal to false.
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(data, &presence); err == nil {
		if _, ok := presence["sync_cables"]; !ok {
			settings.SyncCables = true
		}
		// sync_ip_addresses was added together with IP discovery and should
		// also default to enabled for existing installations.
		if _, ok := presence["sync_ip_addresses"]; !ok {
			settings.SyncIPAddresses = true
		}
	}
	if settings.TokenVersion == "" {
		settings.TokenVersion = "auto"
	}
	switch settings.TokenVersion {
	case "auto", "1", "2":
	default:
		settings.TokenVersion = "auto"
	}
	if settings.PrimaryInterface == "" {
		settings.PrimaryInterface = "mgmt0"
	}
	if settings.UHeight == 0 {
		settings.UHeight = 1
	}
	s.settings = settings
	return nil
}

// legacyTarget carries the pre-v0.5 device-level IP discovery fields so they
// can be migrated into standalone IP targets.
type legacyTarget struct {
	model.Target
	DiscoverIPs bool   `json:"discover_ips"`
	ScanSubnets string `json:"scan_subnets"`
}

func (s *Store) loadTargets() ([]model.IPTarget, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, targetsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read targets: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var items []legacyTarget
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse targets.json: %w", err)
	}
	s.targets = make([]model.Target, 0, len(items))
	var legacy []model.IPTarget
	for _, item := range items {
		model.NormalizeTarget(&item.Target)
		s.targets = append(s.targets, item.Target)
		if !item.DiscoverIPs || item.Target.Protocol != model.ProtocolSNMP {
			continue
		}
		ipTarget := model.IPTarget{
			ID:                      "ip-" + item.Target.ID,
			Name:                    "IP 发现 · " + item.Target.Name,
			Enabled:                 item.Target.Enabled,
			Subnets:                 model.NormalizeSubnets(item.ScanSubnets),
			Address:                 item.Target.Address,
			Port:                    item.Target.Port,
			SNMP:                    item.Target.SNMP,
			ScheduleEnabled:         item.Target.ScheduleEnabled,
			ScheduleIntervalMinutes: item.Target.ScheduleIntervalMinutes,
			LastRunAt:               item.Target.LastRunAt,
		}
		model.NormalizeIPTarget(&ipTarget)
		legacy = append(legacy, ipTarget)
	}
	sort.Slice(s.targets, func(i, j int) bool {
		return s.targets[i].Name < s.targets[j].Name
	})
	return legacy, nil
}

func (s *Store) loadIPTargets() error {
	data, err := os.ReadFile(filepath.Join(s.dir, ipTargetsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read ip targets: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &s.ipTargets); err != nil {
		return fmt.Errorf("parse ip_targets.json: %w", err)
	}
	for i := range s.ipTargets {
		model.NormalizeIPTarget(&s.ipTargets[i])
	}
	s.sortIPTargetsLocked()
	return nil
}

// migrateIPTargets imports device-level IP discovery settings written by
// older releases and rewrites targets.json without the retired fields.
func (s *Store) migrateIPTargets(candidates []model.IPTarget) error {
	existing := make(map[string]bool, len(s.ipTargets))
	for _, target := range s.ipTargets {
		existing[target.ID] = true
	}
	now := time.Now().UTC()
	changed := false
	for _, target := range candidates {
		if existing[target.ID] {
			continue
		}
		if target.CreatedAt.IsZero() {
			target.CreatedAt = now
		}
		if target.UpdatedAt.IsZero() {
			target.UpdatedAt = now
		}
		s.ipTargets = append(s.ipTargets, target)
		existing[target.ID] = true
		changed = true
	}
	if !changed {
		// The migrated target may already exist while targets.json still
		// carries the retired fields (for example after an interrupted
		// migration). Rewrite it anyway so the fields are removed.
		return writeJSON(filepath.Join(s.dir, targetsFile), s.targets)
	}
	s.sortIPTargetsLocked()
	if err := writeJSON(filepath.Join(s.dir, ipTargetsFile), s.ipTargets); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir, targetsFile), s.targets)
}

func (s *Store) sortIPTargetsLocked() {
	sort.Slice(s.ipTargets, func(i, j int) bool {
		return s.ipTargets[i].Name < s.ipTargets[j].Name
	})
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// UpdateSettings replaces the settings and persists them.
func (s *Store) UpdateSettings(settings model.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if settings.TokenVersion == "" {
		settings.TokenVersion = "auto"
	}
	if settings.PrimaryInterface == "" {
		settings.PrimaryInterface = "mgmt0"
	}
	if settings.UHeight == 0 {
		settings.UHeight = 1
	}
	if settings.DeviceRole == "" {
		settings.DeviceRole = "server"
	}
	s.settings = settings
	return s.saveSettingsLocked()
}

// Bootstrap applies environment-provided defaults without overriding values
// already configured in the UI.
func (s *Store) Bootstrap(netboxURL, token, site, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	if s.settings.NetboxURL == "" && netboxURL != "" {
		s.settings.NetboxURL = netboxURL
		changed = true
	}
	if s.settings.NetboxToken == "" && token != "" {
		s.settings.NetboxToken = token
		changed = true
	}
	if s.settings.Site == "" && site != "" {
		s.settings.Site = site
		changed = true
	}
	if s.settings.DeviceRole == "" && role != "" {
		s.settings.DeviceRole = role
		changed = true
	}
	// DeviceRole has a built-in default, so allow the initial env/flag value to
	// override it only on a fresh installation.
	if !s.settingsExisted && role != "" && s.settings.DeviceRole != role {
		s.settings.DeviceRole = role
		changed = true
	}
	if changed {
		return s.saveSettingsLocked()
	}
	return nil
}

func (s *Store) saveSettingsLocked() error {
	return writeJSON(filepath.Join(s.dir, settingsFile), s.settings)
}

// Targets returns a sorted copy of all targets.
func (s *Store) Targets() []model.Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Target, len(s.targets))
	copy(out, s.targets)
	return out
}

// Target returns one target by ID.
func (s *Store) Target(id string) (model.Target, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.targets {
		if t.ID == id {
			return t, true
		}
	}
	return model.Target{}, false
}

// SaveTarget creates or replaces a target and persists the list.
func (s *Store) SaveTarget(t model.Target) (model.Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if t.ID == "" {
		t.ID = newID()
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	found := false
	for i := range s.targets {
		if s.targets[i].ID == t.ID {
			if t.CreatedAt.IsZero() {
				t.CreatedAt = s.targets[i].CreatedAt
			}
			s.targets[i] = t
			found = true
			break
		}
	}
	if !found {
		s.targets = append(s.targets, t)
	}
	sort.Slice(s.targets, func(i, j int) bool {
		return s.targets[i].Name < s.targets[j].Name
	})
	if err := writeJSON(filepath.Join(s.dir, targetsFile), s.targets); err != nil {
		return model.Target{}, err
	}
	return t, nil
}

// MarkTargetRun records when a scheduled run started and persists it so the
// schedule survives restarts. It is a no-op for deleted targets.
func (s *Store) MarkTargetRun(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at = at.UTC()
	for i := range s.targets {
		if s.targets[i].ID != id {
			continue
		}
		s.targets[i].LastRunAt = &at
		return writeJSON(filepath.Join(s.dir, targetsFile), s.targets)
	}
	return nil
}

// DeleteTarget removes a target by ID.
func (s *Store) DeleteTarget(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.targets[:0]
	found := false
	for _, t := range s.targets {
		if t.ID == id {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		return false, nil
	}
	s.targets = out
	return true, writeJSON(filepath.Join(s.dir, targetsFile), s.targets)
}

// IPTargets returns a sorted copy of all IP discovery targets.
func (s *Store) IPTargets() []model.IPTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.IPTarget, len(s.ipTargets))
	copy(out, s.ipTargets)
	return out
}

// IPTarget returns one IP discovery target by ID.
func (s *Store) IPTarget(id string) (model.IPTarget, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.ipTargets {
		if t.ID == id {
			return t, true
		}
	}
	return model.IPTarget{}, false
}

// SaveIPTarget creates or replaces an IP discovery target.
func (s *Store) SaveIPTarget(t model.IPTarget) (model.IPTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if t.ID == "" {
		t.ID = newID()
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	found := false
	for i := range s.ipTargets {
		if s.ipTargets[i].ID == t.ID {
			if t.CreatedAt.IsZero() {
				t.CreatedAt = s.ipTargets[i].CreatedAt
			}
			s.ipTargets[i] = t
			found = true
			break
		}
	}
	if !found {
		s.ipTargets = append(s.ipTargets, t)
	}
	s.sortIPTargetsLocked()
	if err := writeJSON(filepath.Join(s.dir, ipTargetsFile), s.ipTargets); err != nil {
		return model.IPTarget{}, err
	}
	return t, nil
}

// MarkIPTargetRun records when a scheduled IP run started.
func (s *Store) MarkIPTargetRun(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at = at.UTC()
	for i := range s.ipTargets {
		if s.ipTargets[i].ID != id {
			continue
		}
		s.ipTargets[i].LastRunAt = &at
		return writeJSON(filepath.Join(s.dir, ipTargetsFile), s.ipTargets)
	}
	return nil
}

// DeleteIPTarget removes an IP discovery target by ID.
func (s *Store) DeleteIPTarget(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.ipTargets[:0]
	found := false
	for _, t := range s.ipTargets {
		if t.ID == id {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		return false, nil
	}
	s.ipTargets = out
	return true, writeJSON(filepath.Join(s.dir, ipTargetsFile), s.ipTargets)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}