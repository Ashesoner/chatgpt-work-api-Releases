package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	healthMonitorInterval = 2 * time.Second
	healthURLGrace        = 10 * time.Second
	pollStaleAfter        = 75 * time.Second
	localHealthTimeout    = 1500 * time.Millisecond
	proxyProbeTimeout     = 700 * time.Millisecond
)

type healthObservation struct {
	lastSuccessfulPoll float64
	pollErrors         float64
	ready              bool
}

func (m *Manager) monitorConnection(run *activeProcess, httpProxy string) {
	if m == nil || run == nil {
		return
	}
	ticker := time.NewTicker(healthMonitorInterval)
	defer ticker.Stop()

	var previousErrors float64
	var previousSuccess float64
	initialized := false
	check := func() bool {
		now := time.Now()
		observation, err := readTunnelHealth(m.healthURLPath())
		state := "starting"
		if err != nil {
			if now.Sub(run.startedAt) >= healthURLGrace {
				state = "health_unavailable"
			}
		} else {
			state = classifyConnection(now, run.startedAt, observation, initialized, previousErrors, previousSuccess)
			if state == "unreachable" {
				state = unreachableConnectionState(httpProxy)
			}
			previousErrors = observation.pollErrors
			previousSuccess = observation.lastSuccessfulPoll
			initialized = true
		}

		m.mu.Lock()
		defer m.mu.Unlock()
		if m.process != run {
			return false
		}
		if m.snapshot.State == "running" {
			m.snapshot.ConnectionState = state
		}
		return true
	}

	if !check() {
		return
	}
	for {
		select {
		case <-run.done:
			return
		case <-ticker.C:
			if !check() {
				return
			}
		}
	}
}

func classifyConnection(now, startedAt time.Time, observation healthObservation, initialized bool, previousErrors, previousSuccess float64) string {
	newPollFailure := initialized && observation.pollErrors > previousErrors && observation.lastSuccessfulPoll <= previousSuccess
	freshSuccess := false
	if observation.lastSuccessfulPoll > 0 {
		lastSuccess := time.Unix(int64(observation.lastSuccessfulPoll), 0)
		freshSuccess = now.Sub(lastSuccess) <= pollStaleAfter
	}
	switch {
	case newPollFailure:
		return "unreachable"
	case freshSuccess && !observation.ready:
		return "mcp_unready"
	case freshSuccess:
		return "connected"
	case observation.lastSuccessfulPoll == 0 && observation.pollErrors == 0 && now.Sub(startedAt) < pollStaleAfter:
		return "starting"
	default:
		return "unreachable"
	}
}

func readTunnelHealth(path string) (healthObservation, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return healthObservation{}, err
	}
	baseURL := strings.TrimSpace(string(payload))
	if err := validateLocalHealthURL(baseURL); err != nil {
		return healthObservation{}, err
	}
	metrics, err := fetchLocalHealth(baseURL + "/metrics")
	if err != nil {
		return healthObservation{}, err
	}
	lastSuccess, _ := metricValue(metrics, "commands_poll_last_successful_timestamp_seconds")
	pollErrors := metricSum(metrics, "commands_poll_errors_total")
	ready, err := fetchReady(baseURL + "/readyz")
	if err != nil {
		return healthObservation{}, err
	}
	return healthObservation{lastSuccessfulPoll: lastSuccess, pollErrors: pollErrors, ready: ready}, nil
}

func validateLocalHealthURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("OPENAI_TUNNEL_HEALTH_URL_INVALID")
	}
	host := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("OPENAI_TUNNEL_HEALTH_URL_NOT_LOOPBACK")
	}
	return nil
}

func fetchLocalHealth(rawURL string) (string, error) {
	client := localHealthClient()
	response, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("health endpoint status %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func fetchReady(rawURL string) (bool, error) {
	client := localHealthClient()
	response, err := client.Get(rawURL)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode == http.StatusOK, nil
}

func localHealthClient() *http.Client {
	return &http.Client{
		Timeout: localHealthTimeout,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: (&net.Dialer{Timeout: localHealthTimeout}).DialContext,
		},
	}
}

func metricValue(metrics string, name string) (float64, bool) {
	for _, line := range strings.Split(metrics, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, name) {
			continue
		}
		if len(line) > len(name) && line[len(name)] != '{' && line[len(name)] != ' ' && line[len(name)] != '\t' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

func metricSum(metrics string, name string) float64 {
	var total float64
	for _, line := range strings.Split(metrics, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, name) {
			continue
		}
		if len(line) > len(name) && line[len(name)] != '{' && line[len(name)] != ' ' && line[len(name)] != '\t' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			total += value
		}
	}
	return total
}

func unreachableConnectionState(httpProxy string) string {
	if strings.TrimSpace(httpProxy) == "" {
		return "control_plane_unreachable"
	}
	if proxyReachable(httpProxy) {
		return "control_plane_unreachable"
	}
	return "proxy_unreachable"
}

func proxyReachable(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), proxyProbeTimeout)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
