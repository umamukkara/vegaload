package safety

import (
	"testing"
	"time"
)

func TestCheckLimits(t *testing.T) {
	limits := Limits{MaxVUs: 100, MaxDuration: time.Hour, MaxRate: 1000}

	if err := CheckLimits(10, time.Minute, 5, limits); err != nil {
		t.Errorf("within limits: unexpected error: %v", err)
	}
	if err := CheckLimits(101, time.Minute, 5, limits); err == nil {
		t.Error("vus over cap: expected an error")
	}
	if err := CheckLimits(10, 2*time.Hour, 5, limits); err == nil {
		t.Error("duration over cap: expected an error")
	}
	if err := CheckLimits(10, time.Minute, 1001, limits); err == nil {
		t.Error("rate over cap: expected an error")
	}
}

func TestTargetHost(t *testing.T) {
	cases := map[string]string{
		"":                                "",
		"http://localhost:8080/health":    "localhost",
		"https://example.com/":            "example.com",
		"grpc://api.internal:9090":        "api.internal",
		"api.internal:9090":               "api.internal",
		"ws://127.0.0.1:8080/ws":          "127.0.0.1",
		"mqtt://broker.internal:1883":     "broker.internal",
		"postgres://db.internal:5432/app": "db.internal",
		"postgresql://u@db.internal/app":  "db.internal",
		"mqtts://broker.internal":         "broker.internal",
		"kafka://k1.internal:9092":        "k1.internal",
		"tcp://10.0.0.5:7000":             "10.0.0.5",
		"udp://[::1]:5353":                "::1",
	}
	for raw, want := range cases {
		got, err := TargetHost(raw)
		if err != nil {
			t.Errorf("TargetHost(%q) returned error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("TargetHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestIsAllowed(t *testing.T) {
	cases := []struct {
		host  string
		extra []string
		want  bool
	}{
		{"", nil, true},
		{"localhost", nil, true},
		{"127.0.0.1", nil, true},
		{"::1", nil, true},
		{"example.com", nil, false},
		{"example.com", []string{"example.com"}, true},
		{"example.com", []string{"EXAMPLE.COM"}, true},
		{"example.com", []string{"example.com:8080"}, true},
		{"api.internal", []string{"other.internal"}, false},
	}
	for _, c := range cases {
		if got := IsAllowed(c.host, c.extra); got != c.want {
			t.Errorf("IsAllowed(%q, %v) = %v, want %v", c.host, c.extra, got, c.want)
		}
	}
}

// A non-HTTP target must meet the same allowlist as an HTTP one: the
// rule is about the host, not the protocol.
func TestAllowlistAppliesToEveryScheme(t *testing.T) {
	for _, raw := range []string{
		"mqtt://broker.example.com:1883", "kafka://k1.example.com:9092",
		"tcp://db.example.com:5432", "udp://dns.example.com:53",
		"postgres://db.example.com:5432/app",
	} {
		host, err := TargetHost(raw)
		if err != nil {
			t.Fatalf("TargetHost(%q): %v", raw, err)
		}
		if IsAllowed(host, nil) {
			t.Errorf("%q should need confirmation", raw)
		}
		if !IsAllowed(host, []string{host}) {
			t.Errorf("%q should pass once allowlisted", raw)
		}
	}
	for _, raw := range []string{"mqtt://localhost:1883", "tcp://127.0.0.1:7000", "udp://[::1]:53"} {
		host, _ := TargetHost(raw)
		if !IsAllowed(host, nil) {
			t.Errorf("%q is local and should be allowed", raw)
		}
	}
}
