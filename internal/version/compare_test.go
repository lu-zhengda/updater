package version

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer minor", "1.0.0", "1.1.0", true},
		{"same version", "1.0.0", "1.0.0", false},
		{"older major", "2.0.0", "1.0.0", false},
		{"v prefix newer", "v1.0.0", "v1.1.0", true},
		{"v prefix same", "v1.0.0", "v1.0.0", false},
		{"two-part newer", "1.0", "1.1", true},
		{"two-part same", "1.0", "1.0", false},
		{"two-part older", "2.0", "1.0", false},
		{"build numbers newer", "100", "200", true},
		{"build numbers same", "100", "100", false},
		{"build numbers older", "200", "100", false},
		{"newer patch", "1.0.0", "1.0.1", true},
		{"newer major", "1.0.0", "2.0.0", true},
		{"mixed v prefix", "v1.0.0", "1.1.0", true},
		{"mixed v prefix reverse", "1.0.0", "v1.1.0", true},
		{"four-part newer", "1.0.0.0", "1.0.0.1", true},
		{"four-part same", "1.0.0.0", "1.0.0.0", false},
		{"pre-release newer", "1.0.0-beta", "1.0.0", true},
		{"empty current", "", "1.0.0", true},
		{"empty latest", "1.0.0", "", false},
		{"both empty", "", "", false},
		{"non-numeric single part newer", "alpha", "beta", true},
		{"non-numeric single part older", "beta", "alpha", false},
		{"non-numeric single part same", "alpha", "alpha", false},
		{"four-part older", "1.0.0.2", "1.0.0.1", false},
		{"non-numeric segment newer", "1.0.a", "1.0.b", true},
		{"non-numeric segment older", "1.0.b", "1.0.a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNewer(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v",
					tt.current, tt.latest, got, tt.want)
			}
		})
	}
}
