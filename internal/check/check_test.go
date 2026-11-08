package check

import "testing"

func TestMatchEgress(t *testing.T) {
	cases := []struct {
		ip, spec string
		want     bool
	}{
		{"192.0.2.1", "192.0.2.1", true},
		{"192.0.2.1", "1.2.3.4, 192.0.2.1", true},
		{"192.0.2.1", "192.0.2.0/24", true},
		{"198.51.100.7", "192.0.2.0/24 1.2.3.4", false},
		{"203.0.113.9", "192.0.2.1", false},
	}
	for _, c := range cases {
		got, err := MatchEgress(c.ip, c.spec)
		if err != nil || got != c.want {
			t.Errorf("MatchEgress(%q, %q) = %v, %v; want %v", c.ip, c.spec, got, err, c.want)
		}
	}
}

func TestMatchEgressRejectsBadSpec(t *testing.T) {
	for _, spec := range []string{"", " , ", "not-an-ip", "10.0.0.0/99"} {
		if _, err := MatchEgress("1.2.3.4", spec); err == nil {
			t.Errorf("spec %q: expected an error", spec)
		}
	}
}
