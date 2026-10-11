package tunnelnetwork

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	Schema        = "cwapi.tunnel-network.v1"
	ModeDirect    = "direct"
	ModeHTTPProxy = "http_proxy"
	maxFileBytes  = 64 * 1024
)

type Endpoint struct {
	Mode      string `json:"mode"`
	HTTPProxy string `json:"http_proxy,omitempty"`
}

type State struct {
	Schema string   `json:"schema"`
	Coding Endpoint `json:"coding"`
	Agent  Endpoint `json:"agent"`
}

func Default() State {
	return State{
		Schema: Schema,
		Coding: Endpoint{Mode: ModeDirect},
		Agent:  Endpoint{Mode: ModeDirect},
	}
}

func PathForConfig(configPath string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(configPath)), "tunnel-network.json")
}

func ProxyURL(endpoint Endpoint) string {
	if endpoint.Mode == ModeHTTPProxy {
		return endpoint.HTTPProxy
	}
	return ""
}

func Validate(state State) error {
	if state.Schema != Schema {
		return fmt.Errorf("TUNNEL_NETWORK_SCHEMA_UNSUPPORTED: %q", state.Schema)
	}
	if err := ValidateEndpoint(state.Coding); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_CODING_INVALID: %w", err)
	}
	if err := ValidateEndpoint(state.Agent); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_AGENT_INVALID: %w", err)
	}
	return nil
}

func ValidateEndpoint(endpoint Endpoint) error {
	if endpoint.Mode != strings.TrimSpace(endpoint.Mode) || endpoint.HTTPProxy != strings.TrimSpace(endpoint.HTTPProxy) {
		return errors.New("TUNNEL_NETWORK_VALUE_INVALID")
	}
	switch endpoint.Mode {
	case ModeDirect:
		if endpoint.HTTPProxy != "" {
			return errors.New("TUNNEL_NETWORK_DIRECT_PROXY_MUST_BE_EMPTY")
		}
		return nil
	case ModeHTTPProxy:
		return ValidateHTTPProxy(endpoint.HTTPProxy)
	default:
		return errors.New("TUNNEL_NETWORK_MODE_INVALID")
	}
}

func ValidateHTTPProxy(value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return errors.New("TUNNEL_NETWORK_HTTP_PROXY_INVALID")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" {
		return errors.New("TUNNEL_NETWORK_HTTP_PROXY_INVALID")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("TUNNEL_NETWORK_HTTP_PROXY_SCHEME_INVALID")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("TUNNEL_NETWORK_HTTP_PROXY_INVALID")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("TUNNEL_NETWORK_HTTP_PROXY_INVALID")
		}
	}
	return nil
}

func LoadOrDefault(path string) (State, error) {
	state, err := Load(path)
	if err == nil {
		return state, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	return State{}, err
}

func Load(path string) (State, error) {
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return State{}, fmt.Errorf("TUNNEL_NETWORK_READ_FAILED: %w", err)
	}
	if len(payload) > maxFileBytes {
		return State{}, errors.New("TUNNEL_NETWORK_FILE_TOO_LARGE")
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("TUNNEL_NETWORK_DECODE_FAILED: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return State{}, errors.New("TUNNEL_NETWORK_TRAILING_DATA")
	}
	if err := Validate(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func SaveAtomic(path string, state State) error {
	if err := Validate(state); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_DIRECTORY_CREATE_FAILED: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".tunnel-network-*.tmp")
	if err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_TEMP_CREATE_FAILED: %w", err)
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
		return fmt.Errorf("TUNNEL_NETWORK_TEMP_PERMISSION_FAILED: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_ENCODE_FAILED: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_TEMP_SYNC_FAILED: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_TEMP_CLOSE_FAILED: %w", err)
	}
	if err := replaceFileAtomic(temporaryPath, path); err != nil {
		return fmt.Errorf("TUNNEL_NETWORK_ATOMIC_REPLACE_FAILED: %w", err)
	}
	committed = true
	return nil
}
