package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/credentials"
	"github.com/AAAYNMMM/CWapi/internal/tunnelprofiles"
	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
)

type TunnelProfileInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TunnelID      string `json:"tunnel_id"`
	APIKeyPresent bool   `json:"api_key_present"`
	Active        bool   `json:"active"`
	State         string `json:"state"`
}

type tunnelSelection struct {
	enabled bool
	profile tunnelprofiles.Profile
	key     string
}

func (a *App) CodingTunnelProfiles() ([]TunnelProfileInfo, error) {
	return a.tunnelProfileInfos(false)
}

func (a *App) AgentTunnelProfiles() ([]TunnelProfileInfo, error) {
	return a.tunnelProfileInfos(true)
}

func (a *App) SaveCodingTunnelProfile(profileID, name, tunnelID, apiKey string) ([]TunnelProfileInfo, error) {
	return a.saveTunnelProfile(false, profileID, name, tunnelID, apiKey)
}

func (a *App) SaveAgentTunnelProfile(profileID, name, tunnelID, apiKey string) ([]TunnelProfileInfo, error) {
	return a.saveTunnelProfile(true, profileID, name, tunnelID, apiKey)
}

func (a *App) ActivateCodingTunnelProfile(profileID string) ([]TunnelProfileInfo, error) {
	return a.activateTunnelProfile(false, profileID)
}

func (a *App) ActivateAgentTunnelProfile(profileID string) ([]TunnelProfileInfo, error) {
	return a.activateTunnelProfile(true, profileID)
}

func (a *App) DeactivateCodingTunnelProfile() ([]TunnelProfileInfo, error) {
	return a.deactivateTunnelProfile(false)
}

func (a *App) DeactivateAgentTunnelProfile() ([]TunnelProfileInfo, error) {
	return a.deactivateTunnelProfile(true)
}

func (a *App) DeleteCodingTunnelProfile(profileID string) ([]TunnelProfileInfo, error) {
	return a.deleteTunnelProfile(false, profileID)
}

func (a *App) DeleteAgentTunnelProfile(profileID string) ([]TunnelProfileInfo, error) {
	return a.deleteTunnelProfile(true, profileID)
}

func (a *App) ensureTunnelProfiles() error {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()
	_, err := a.ensureTunnelProfilesLocked()
	return err
}

func (a *App) ensureTunnelProfilesLocked() (tunnelprofiles.State, error) {
	a.mu.RLock()
	configPath := a.configPath
	a.mu.RUnlock()
	if strings.TrimSpace(configPath) == "" {
		return tunnelprofiles.State{}, errors.New("CORE_CONFIG_PATH_UNAVAILABLE")
	}
	path := tunnelprofiles.PathForConfig(configPath)
	state, err := tunnelprofiles.LoadOrCreate(path)
	if err != nil {
		return tunnelprofiles.State{}, err
	}
	cfg, err := v2config.Load(configPath)
	if err != nil {
		return tunnelprofiles.State{}, err
	}
	manager := credentials.New()
	changed := false
	if imported, importErr := importCurrentTunnel(&state.Coding, cfg.Tunnel, false, manager); importErr != nil {
		return tunnelprofiles.State{}, importErr
	} else {
		changed = changed || imported
	}
	if imported, importErr := importCurrentTunnel(&state.Agent, cfg.AgentTunnel, true, manager); importErr != nil {
		return tunnelprofiles.State{}, importErr
	} else {
		changed = changed || imported
	}
	if changed {
		if err := tunnelprofiles.Save(path, state); err != nil {
			return tunnelprofiles.State{}, err
		}
	}
	return state, nil
}

func importCurrentTunnel(surface *tunnelprofiles.SurfaceState, config v2config.TunnelConfig, agent bool, manager *credentials.Manager) (bool, error) {
	if surface == nil || !config.Enabled || config.TunnelID == "" {
		return false, nil
	}
	if surface.Active != "" {
		if active, ok := tunnelprofiles.Find(*surface, surface.Active); ok && active.TunnelID == config.TunnelID {
			return false, nil
		}
	}
	for _, profile := range surface.Profiles {
		if profile.TunnelID != config.TunnelID {
			continue
		}
		_, present, err := readSavedProfileKey(manager, agent, profile.ID)
		if err != nil {
			return false, err
		}
		if !present {
			key, mirrorPresent, err := readMirrorKey(manager, agent)
			if err != nil {
				return false, err
			}
			if mirrorPresent {
				if err := writeSavedProfileKey(manager, agent, profile.ID, key); err != nil {
					return false, err
				}
			}
		}
		surface.Active = profile.ID
		return true, nil
	}
	key, present, err := readMirrorKey(manager, agent)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	profile := tunnelprofiles.Profile{
		ID:       tunnelprofiles.NewProfileID(),
		Name:     fmt.Sprintf("账号 %d", len(surface.Profiles)+1),
		TunnelID: config.TunnelID,
	}
	if err := tunnelprofiles.Upsert(surface, profile); err != nil {
		return false, err
	}
	if err := writeSavedProfileKey(manager, agent, profile.ID, key); err != nil {
		return false, err
	}
	surface.Active = profile.ID
	return true, nil
}

func (a *App) tunnelProfileInfos(agent bool) ([]TunnelProfileInfo, error) {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()
	state, err := a.ensureTunnelProfilesLocked()
	if err != nil {
		return nil, err
	}
	return a.tunnelProfileInfosLocked(state, agent)
}

func (a *App) tunnelProfileInfosLocked(state tunnelprofiles.State, agent bool) ([]TunnelProfileInfo, error) {
	manager := credentials.New()
	surface := profileSurface(&state, agent)
	activeState := "disabled"
	snapshot := a.RuntimeSnapshot()
	if agent {
		activeState = snapshot.AgentOpenAITunnel.State
	} else {
		activeState = snapshot.OpenAITunnel.State
	}
	result := make([]TunnelProfileInfo, 0, len(surface.Profiles))
	for _, profile := range surface.Profiles {
		_, present, err := readSavedProfileKey(manager, agent, profile.ID)
		if err != nil {
			return nil, err
		}
		stateLabel := "saved"
		active := surface.Active == profile.ID
		if active {
			stateLabel = activeState
		}
		result = append(result, TunnelProfileInfo{
			ID: profile.ID, Name: profile.Name, TunnelID: profile.TunnelID,
			APIKeyPresent: present, Active: active, State: stateLabel,
		})
	}
	return result, nil
}

func (a *App) saveTunnelProfile(agent bool, profileID, name, tunnelID, apiKey string) ([]TunnelProfileInfo, error) {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()
	state, err := a.ensureTunnelProfilesLocked()
	if err != nil {
		return nil, err
	}
	before := cloneProfileState(state)
	surface := profileSurface(&state, agent)
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = tunnelprofiles.NewProfileID()
	}
	profile := tunnelprofiles.Profile{ID: profileID, Name: strings.TrimSpace(name), TunnelID: strings.TrimSpace(tunnelID)}
	if err := tunnelprofiles.ValidateProfile(profile); err != nil {
		return nil, err
	}
	manager := credentials.New()
	oldSelection, err := selectionFromSurface(profileSurface(&before, agent), manager, agent)
	if err != nil {
		return nil, err
	}
	oldKey, oldPresent, err := readSavedProfileKey(manager, agent, profile.ID)
	if err != nil {
		return nil, err
	}
	key := oldKey
	if strings.TrimSpace(apiKey) != "" {
		if err := credentials.ValidateOpenAITunnelAPIKey(apiKey); err != nil {
			return nil, err
		}
		key = apiKey
		if err := writeSavedProfileKey(manager, agent, profile.ID, apiKey); err != nil {
			return nil, err
		}
	} else if !oldPresent {
		return nil, errors.New("OPENAI_TUNNEL_API_KEY_REQUIRED")
	}
	if err := tunnelprofiles.Upsert(surface, profile); err != nil {
		restoreSavedProfileKey(manager, agent, profile.ID, oldKey, oldPresent)
		return nil, err
	}
	if err := a.saveTunnelProfileStateLocked(state); err != nil {
		restoreSavedProfileKey(manager, agent, profile.ID, oldKey, oldPresent)
		return nil, err
	}
	if surface.Active == profile.ID {
		newSelection := tunnelSelection{enabled: true, profile: profile, key: key}
		if err := a.applyTunnelSelection(agent, newSelection, oldSelection); err != nil {
			_ = a.saveTunnelProfileStateLocked(before)
			restoreSavedProfileKey(manager, agent, profile.ID, oldKey, oldPresent)
			return nil, err
		}
	}
	return a.tunnelProfileInfosLocked(state, agent)
}

func (a *App) activateTunnelProfile(agent bool, profileID string) ([]TunnelProfileInfo, error) {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()
	state, err := a.ensureTunnelProfilesLocked()
	if err != nil {
		return nil, err
	}
	before := cloneProfileState(state)
	surface := profileSurface(&state, agent)
	profile, ok := tunnelprofiles.Find(*surface, strings.TrimSpace(profileID))
	if !ok {
		return nil, errors.New("TUNNEL_PROFILE_NOT_FOUND")
	}
	manager := credentials.New()
	key, present, err := readSavedProfileKey(manager, agent, profile.ID)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("OPENAI_TUNNEL_API_KEY_MISSING")
	}
	oldSelection, err := selectionFromSurface(profileSurface(&before, agent), manager, agent)
	if err != nil {
		return nil, err
	}
	newSelection := tunnelSelection{enabled: true, profile: profile, key: key}
	if err := a.applyTunnelSelection(agent, newSelection, oldSelection); err != nil {
		return nil, err
	}
	if surface.Active != profile.ID {
		surface.Active = profile.ID
		if err := a.saveTunnelProfileStateLocked(state); err != nil {
			_ = a.applyTunnelSelection(agent, oldSelection, newSelection)
			return nil, err
		}
	}
	return a.tunnelProfileInfosLocked(state, agent)
}

func (a *App) deactivateTunnelProfile(agent bool) ([]TunnelProfileInfo, error) {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()
	state, err := a.ensureTunnelProfilesLocked()
	if err != nil {
		return nil, err
	}
	surface := profileSurface(&state, agent)
	manager := credentials.New()
	oldSelection, err := selectionFromSurface(surface, manager, agent)
	if err != nil {
		return nil, err
	}
	if !oldSelection.enabled {
		return a.tunnelProfileInfosLocked(state, agent)
	}
	disabled := tunnelSelection{}
	if err := a.applyTunnelSelection(agent, disabled, oldSelection); err != nil {
		return nil, err
	}
	surface.Active = ""
	if err := a.saveTunnelProfileStateLocked(state); err != nil {
		_ = a.applyTunnelSelection(agent, oldSelection, disabled)
		return nil, err
	}
	return a.tunnelProfileInfosLocked(state, agent)
}

func (a *App) deleteTunnelProfile(agent bool, profileID string) ([]TunnelProfileInfo, error) {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()
	state, err := a.ensureTunnelProfilesLocked()
	if err != nil {
		return nil, err
	}
	before := cloneProfileState(state)
	surface := profileSurface(&state, agent)
	profileID = strings.TrimSpace(profileID)
	if _, ok := tunnelprofiles.Find(*surface, profileID); !ok {
		return nil, errors.New("TUNNEL_PROFILE_NOT_FOUND")
	}
	manager := credentials.New()
	oldSelection, selectionErr := selectionFromSurface(profileSurface(&before, agent), manager, agent)
	if selectionErr != nil {
		return nil, selectionErr
	}
	wasActive := surface.Active == profileID
	if wasActive {
		if err := a.applyTunnelSelection(agent, tunnelSelection{}, oldSelection); err != nil {
			return nil, err
		}
		surface.Active = ""
	}
	tunnelprofiles.Remove(surface, profileID)
	if err := a.saveTunnelProfileStateLocked(state); err != nil {
		if wasActive {
			_ = a.applyTunnelSelection(agent, oldSelection, tunnelSelection{})
		}
		return nil, err
	}
	if err := deleteSavedProfileKey(manager, agent, profileID); err != nil {
		return nil, err
	}
	return a.tunnelProfileInfosLocked(state, agent)
}
func (a *App) applyTunnelSelection(agent bool, next, rollback tunnelSelection) error {
	manager := credentials.New()
	oldMirror, oldMirrorPresent, err := readMirrorKey(manager, agent)
	if err != nil {
		return err
	}
	if err := a.applyTunnelRuntime(agent, next); err != nil {
		return err
	}
	if err := writeMirrorSelection(manager, agent, next); err != nil {
		rollbackErr := a.applyTunnelRuntime(agent, rollback)
		restoreErr := restoreMirrorKey(manager, agent, oldMirror, oldMirrorPresent)
		return errors.Join(err, rollbackErr, restoreErr)
	}
	return nil
}

func (a *App) applyTunnelRuntime(agent bool, selection tunnelSelection) error {
	service, err := a.core()
	if err != nil {
		return err
	}
	config := v2config.TunnelConfig{}
	key := ""
	if selection.enabled {
		config = v2config.TunnelConfig{Enabled: true, TunnelID: selection.profile.TunnelID}
		key = selection.key
	}
	if agent {
		_, err = service.UpdateAgentOpenAITunnel(config, key)
	} else {
		_, err = service.UpdateCodingOpenAITunnel(config, key)
	}
	return err
}

func selectionFromSurface(surface *tunnelprofiles.SurfaceState, manager *credentials.Manager, agent bool) (tunnelSelection, error) {
	if surface == nil || surface.Active == "" {
		return tunnelSelection{}, nil
	}
	profile, ok := tunnelprofiles.Find(*surface, surface.Active)
	if !ok {
		return tunnelSelection{}, errors.New("TUNNEL_PROFILE_ACTIVE_MISSING")
	}
	key, present, err := readSavedProfileKey(manager, agent, profile.ID)
	if err != nil {
		return tunnelSelection{}, err
	}
	if !present {
		return tunnelSelection{}, errors.New("OPENAI_TUNNEL_API_KEY_MISSING")
	}
	return tunnelSelection{enabled: true, profile: profile, key: key}, nil
}

func (a *App) saveTunnelProfileStateLocked(state tunnelprofiles.State) error {
	a.mu.RLock()
	configPath := a.configPath
	a.mu.RUnlock()
	return tunnelprofiles.Save(tunnelprofiles.PathForConfig(configPath), state)
}

func profileSurface(state *tunnelprofiles.State, agent bool) *tunnelprofiles.SurfaceState {
	if agent {
		return &state.Agent
	}
	return &state.Coding
}

func cloneProfileState(state tunnelprofiles.State) tunnelprofiles.State {
	clone := state
	clone.Coding.Profiles = append([]tunnelprofiles.Profile(nil), state.Coding.Profiles...)
	clone.Agent.Profiles = append([]tunnelprofiles.Profile(nil), state.Agent.Profiles...)
	return clone
}

func readSavedProfileKey(manager *credentials.Manager, agent bool, id string) (string, bool, error) {
	if agent {
		return manager.ReadOpenAITunnelAgentProfileAPIKey(id)
	}
	return manager.ReadOpenAITunnelProfileAPIKey(id)
}

func writeSavedProfileKey(manager *credentials.Manager, agent bool, id, key string) error {
	if agent {
		return manager.WriteOpenAITunnelAgentProfileAPIKey(id, key)
	}
	return manager.WriteOpenAITunnelProfileAPIKey(id, key)
}

func deleteSavedProfileKey(manager *credentials.Manager, agent bool, id string) error {
	if agent {
		return manager.DeleteOpenAITunnelAgentProfileAPIKey(id)
	}
	return manager.DeleteOpenAITunnelProfileAPIKey(id)
}

func restoreSavedProfileKey(manager *credentials.Manager, agent bool, id, value string, present bool) {
	if present {
		_ = writeSavedProfileKey(manager, agent, id, value)
	} else {
		_ = deleteSavedProfileKey(manager, agent, id)
	}
}

func readMirrorKey(manager *credentials.Manager, agent bool) (string, bool, error) {
	if agent {
		return manager.ReadOpenAITunnelAgentAPIKey()
	}
	return manager.ReadOpenAITunnelAPIKey()
}

func writeMirrorSelection(manager *credentials.Manager, agent bool, selection tunnelSelection) error {
	if !selection.enabled {
		if agent {
			return manager.DeleteOpenAITunnelAgentAPIKey()
		}
		return manager.DeleteOpenAITunnelAPIKey()
	}
	if agent {
		return manager.WriteOpenAITunnelAgentAPIKey(selection.key)
	}
	return manager.WriteOpenAITunnelAPIKey(selection.key)
}

func restoreMirrorKey(manager *credentials.Manager, agent bool, value string, present bool) error {
	if present {
		if agent {
			return manager.WriteOpenAITunnelAgentAPIKey(value)
		}
		return manager.WriteOpenAITunnelAPIKey(value)
	}
	if agent {
		return manager.DeleteOpenAITunnelAgentAPIKey()
	}
	return manager.DeleteOpenAITunnelAPIKey()
}
