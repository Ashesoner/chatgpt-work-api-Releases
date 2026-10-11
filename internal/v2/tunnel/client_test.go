package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/AAAYNMMM/CWapi/internal/childenv"
	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
)

func TestTunnelEnvironmentProxy(t *testing.T) {
	direct := tunnelEnvironment("key", "")
	if got := childenv.Value(direct, "CONTROL_PLANE_HTTP_PROXY"); got != "" {
		t.Fatalf("direct proxy=%q", got)
	}
	proxied := tunnelEnvironment("key", "http://127.0.0.1:7897")
	if got := childenv.Value(proxied, "CONTROL_PLANE_HTTP_PROXY"); got != "http://127.0.0.1:7897" {
		t.Fatalf("proxy=%q", got)
	}
}

type fakeTunnelProcess struct {
	done chan struct{}
	once sync.Once
}

func (p *fakeTunnelProcess) Wait() error {
	<-p.done
	return nil
}

func (p *fakeTunnelProcess) Kill() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func TestReconfigureHTTPProxyRestartsWithExplicitProxy(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "tunnel-client.exe")
	if err := os.WriteFile(executable, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithProfileAndProxy(
		root,
		executable,
		v2config.TunnelConfig{Enabled: true, TunnelID: "tunnel_0123456789abcdef0123456789abcdef"},
		"runtime-key",
		"http://127.0.0.1:32124/mcp/coding/token",
		"coding",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var environments [][]string
	manager.start = func(_ string, _ []string, environment []string, _ string) (process, error) {
		mu.Lock()
		environments = append(environments, append([]string(nil), environment...))
		mu.Unlock()
		return &fakeTunnelProcess{done: make(chan struct{})}, nil
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconfigureHTTPProxy(context.Background(), "http://127.0.0.1:7897"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(environments) != 2 {
		mu.Unlock()
		t.Fatalf("starts=%d want=2", len(environments))
	}
	first, second := environments[0], environments[1]
	mu.Unlock()
	if got := childenv.Value(first, "CONTROL_PLANE_HTTP_PROXY"); got != "" {
		t.Fatalf("first proxy=%q", got)
	}
	if got := childenv.Value(second, "CONTROL_PLANE_HTTP_PROXY"); got != "http://127.0.0.1:7897" {
		t.Fatalf("second proxy=%q", got)
	}
	if err := manager.ReconfigureHTTPProxy(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(environments) != 3 {
		mu.Unlock()
		t.Fatalf("starts=%d want=3", len(environments))
	}
	third := environments[2]
	mu.Unlock()
	if got := childenv.Value(third, "CONTROL_PLANE_HTTP_PROXY"); got != "" {
		t.Fatalf("cleared proxy=%q", got)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
