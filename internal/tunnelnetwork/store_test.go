package tunnelnetwork

import (
	"path/filepath"
	"testing"
)

func TestEndpointValidation(t *testing.T) {
	cases := []struct {
		name     string
		endpoint Endpoint
		valid    bool
	}{
		{"direct", Endpoint{Mode: ModeDirect}, true},
		{"http", Endpoint{Mode: ModeHTTPProxy, HTTPProxy: "http://127.0.0.1:7897"}, true},
		{"https", Endpoint{Mode: ModeHTTPProxy, HTTPProxy: "https://proxy.example:443"}, true},
		{"socks rejected", Endpoint{Mode: ModeHTTPProxy, HTTPProxy: "socks5://127.0.0.1:7897"}, false},
		{"credentials rejected", Endpoint{Mode: ModeHTTPProxy, HTTPProxy: "http://user:pass@127.0.0.1:7897"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEndpoint(tc.endpoint)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel-network.json")
	state := Default()
	state.Coding = Endpoint{Mode: ModeHTTPProxy, HTTPProxy: "http://127.0.0.1:7897"}
	if err := SaveAtomic(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != state {
		t.Fatalf("loaded=%+v want=%+v", loaded, state)
	}
}
