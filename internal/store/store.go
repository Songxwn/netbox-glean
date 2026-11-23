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
	settingsFile = "settings.json"
	targetsFile  = "targets.json"
)

// Store is a small thread-safe JSON backed store. Credentials are stored in
// plain text with file mode 0600; protect the data directory accordingly.
type Store struct {
	mu              sync.RWMutex
	dir             string
	settings        model.Settings
	targets         []model.Target
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
	if err := s.loadTargets(); err != nil {
		return nil, err
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

func (s *Store) loadTargets() error {
	data, err := os.ReadFile(filepath.Join(s.dir, targetsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read targets: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &s.targets); err != nil {
		return fmt.Errorf("parse targets.json: %w", err)
	}
	for i := range s.targets {
		model.NormalizeTarget(&s.targets[i])
	}
	sort.Slice(s.targets, func(i, j int) bool {
		return s.targets[i].Name < s.targets[j].Name
	})
	return nil
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