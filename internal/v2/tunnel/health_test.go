package tunnel

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadTunnelHealthUsesPollSuccessAndReadiness(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "commands_poll_last_successful_timestamp_seconds{otel_scope_name=\"controlplane\"} %d\n", time.Now().Unix())
		fmt.Fprintln(w, "commands_poll_errors_total{error_kind=\"other\"} 2")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	server := httptest.NewUnstartedServer(mux)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()

	path := filepath.Join(t.TempDir(), "health.url")
	if err := os.WriteFile(path, []byte(server.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	observation, err := readTunnelHealth(path)
	if err != nil {
		t.Fatal(err)
	}
	if observation.lastSuccessfulPoll == 0 || observation.pollErrors != 2 || !observation.ready {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestValidateLocalHealthURLRejectsNonLoopback(t *testing.T) {
	if err := validateLocalHealthURL("http://example.com:8080"); err == nil {
		t.Fatal("expected non-loopback health URL to be rejected")
	}
	if err := validateLocalHealthURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback health URL rejected: %v", err)
	}
}

func TestClassifyConnection(t *testing.T) {
	now := time.Now()
	started := now.Add(-20 * time.Second)
	cases := []struct {
		name            string
		observation     healthObservation
		initialized     bool
		previousErrors  float64
		previousSuccess float64
		want            string
	}{
		{name: "fresh poll", observation: healthObservation{lastSuccessfulPoll: float64(now.Unix()), ready: true}, want: "connected"},
		{name: "fresh poll but local MCP not ready", observation: healthObservation{lastSuccessfulPoll: float64(now.Unix()), ready: false}, want: "mcp_unready"},
		{name: "startup before first poll", observation: healthObservation{}, want: "starting"},
		{name: "poll failure after success", observation: healthObservation{lastSuccessfulPoll: float64(now.Add(-5 * time.Second).Unix()), pollErrors: 4, ready: true}, initialized: true, previousErrors: 3, previousSuccess: float64(now.Add(-5 * time.Second).Unix()), want: "unreachable"},
		{name: "initial errors without success", observation: healthObservation{pollErrors: 1, ready: true}, want: "unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyConnection(now, started, tc.observation, tc.initialized, tc.previousErrors, tc.previousSuccess); got != tc.want {
				t.Fatalf("state=%q want=%q", got, tc.want)
			}
		})
	}
}
