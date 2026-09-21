package tunnelprofiles

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
)

const (
	Schema          = "cwapi.tunnel-profiles.v1"
	maxProfileCount = 16
	maxFileBytes    = 256 * 1024
)

var profileIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)

type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TunnelID string `json:"tunnel_id"`
}

type SurfaceState struct {
	Active   string    `json:"active,omitempty"`
	Profiles []Profile `json:"profiles"`
}

type State struct {
	Schema string       `json:"schema"`
	Coding SurfaceState `json:"coding"`
	Agent  SurfaceState `json:"agent"`
}

func Default() State {
	return State{Schema: Schema, Coding: SurfaceState{Profiles: []Profile{}}, Agent: SurfaceState{Profiles: []Profile{}}}
}

func PathForConfig(configPath string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(configPath)), "tunnel-profiles.json")
}

func NewProfileID() string {
	return "acct_" + strings.ToLower(rand.Text())
}

func ValidateProfile(profile Profile) error {
	if !profileIDPattern.MatchString(profile.ID) {
		return errors.New("TUNNEL_PROFILE_ID_INVALID")
	}
	if profile.Name != strings.TrimSpace(profile.Name) || profile.Name == "" || len([]rune(profile.Name)) > 80 {
		return errors.New("TUNNEL_PROFILE_NAME_INVALID")
	}
	if err := v2config.ValidateTunnelID(profile.TunnelID); err != nil {
		return err
	}
	return nil
}

func Validate(state State) error {
	if state.Schema != Schema {
		return fmt.Errorf("TUNNEL_PROFILE_SCHEMA_UNSUPPORTED: %q", state.Schema)
	}
	if err := validateSurface(state.Coding); err != nil {
		return fmt.Errorf("TUNNEL_PROFILE_CODING_INVALID: %w", err)
	}
	if err := validateSurface(state.Agent); err != nil {
		return fmt.Errorf("TUNNEL_PROFILE_AGENT_INVALID: %w", err)
	}
	return nil
}

func validateSurface(surface SurfaceState) error {
	if len(surface.Profiles) > maxProfileCount {
		return errors.New("TUNNEL_PROFILE_LIMIT_EXCEEDED")
	}
	seen := map[string]bool{}
	activeFound := surface.Active == ""
	for _, profile := range surface.Profiles {
		if err := ValidateProfile(profile); err != nil {
			return err
		}
		if seen[profile.ID] {
			return errors.New("TUNNEL_PROFILE_ID_DUPLICATE")
		}
		seen[profile.ID] = true
		if profile.ID == surface.Active {
			activeFound = true
		}
	}
	if !activeFound {
		return errors.New("TUNNEL_PROFILE_ACTIVE_MISSING")
	}
	return nil
}

func Find(surface SurfaceState, id string) (Profile, bool) {
	for _, profile := range surface.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return Profile{}, false
}

func Upsert(surface *SurfaceState, profile Profile) error {
	if surface == nil {
		return errors.New("TUNNEL_PROFILE_SURFACE_REQUIRED")
	}
	if err := ValidateProfile(profile); err != nil {
		return err
	}
	for i := range surface.Profiles {
		if surface.Profiles[i].ID == profile.ID {
			surface.Profiles[i] = profile
			return nil
		}
	}
	if len(surface.Profiles) >= maxProfileCount {
		return errors.New("TUNNEL_PROFILE_LIMIT_EXCEEDED")
	}
	surface.Profiles = append(surface.Profiles, profile)
	return nil
}

func Remove(surface *SurfaceState, id string) bool {
	if surface == nil {
		return false
	}
	for i := range surface.Profiles {
		if surface.Profiles[i].ID != id {
			continue
		}
		surface.Profiles = append(surface.Profiles[:i], surface.Profiles[i+1:]...)
		if surface.Active == id {
			surface.Active = ""
		}
		return true
	}
	return false
}

func LoadOrCreate(path string) (State, error) {
	state, err := Load(path)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}
	state = Default()
	if err := Save(path, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func Load(path string) (State, error) {
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return State{}, fmt.Errorf("TUNNEL_PROFILE_READ_FAILED: %w", err)
	}
	if len(payload) > maxFileBytes {
		return State{}, errors.New("TUNNEL_PROFILE_FILE_TOO_LARGE")
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("TUNNEL_PROFILE_DECODE_FAILED: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return State{}, errors.New("TUNNEL_PROFILE_TRAILING_DATA")
	}
	if err := Validate(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func Save(path string, state State) error {
	if err := Validate(state); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("TUNNEL_PROFILE_DIRECTORY_CREATE_FAILED: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".tunnel-profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("TUNNEL_PROFILE_TEMP_CREATE_FAILED: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return fmt.Errorf("TUNNEL_PROFILE_ENCODE_FAILED: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("TUNNEL_PROFILE_REPLACE_FAILED: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("TUNNEL_PROFILE_REPLACE_FAILED: %w", err)
	}
	committed = true
	return nil
}
