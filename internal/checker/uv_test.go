package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestUvChecker_Name(t *testing.T) {
	c := NewUvChecker(nil, "")
	if got := c.Name(); got != "uv" {
		t.Errorf("Name() = %q, want %q", got, "uv")
	}
}

func TestUvChecker_CanCheck(t *testing.T) {
	c := NewUvChecker(nil, "")

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "uv tool",
			app:  &app.App{Source: app.SourceUv, UvTool: "ruff"},
			want: true,
		},
		{
			name: "uv source without tool name",
			app:  &app.App{Source: app.SourceUv},
			want: false,
		},
		{
			name: "npm package",
			app:  &app.App{Source: app.SourceNpm, NpmPackage: "typescript"},
			want: false,
		},
		{
			name: "unknown source with uv tool name set",
			app:  &app.App{Source: app.SourceUnknown, UvTool: "ruff"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.CanCheck(tt.app); got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUvChecker_Check(t *testing.T) {
	tests := []struct {
		name       string
		app        *app.App
		body       string
		status     int
		wantUpdate bool
		wantLatest string
		wantErr    bool
	}{
		{
			name:       "outdated tool",
			app:        &app.App{Name: "ruff", Version: "0.8.0", Source: app.SourceUv, UvTool: "ruff"},
			body:       `{"info":{"version":"0.8.4"}}`,
			status:     200,
			wantUpdate: true,
			wantLatest: "0.8.4",
		},
		{
			name:       "up-to-date tool",
			app:        &app.App{Name: "black", Version: "25.1.0", Source: app.SourceUv, UvTool: "black"},
			body:       `{"info":{"version":"25.1.0"}}`,
			status:     200,
			wantUpdate: false,
			wantLatest: "25.1.0",
		},
		{
			name:    "missing tool name",
			app:     &app.App{Name: "mystery", Source: app.SourceUv},
			wantErr: true,
		},
		{
			name:    "PyPI returns 404",
			app:     &app.App{Name: "ghost", Version: "1.0.0", Source: app.SourceUv, UvTool: "ghost"},
			status:  404,
			wantErr: true,
		},
		{
			name:    "PyPI returns invalid JSON",
			app:     &app.App{Name: "ruff", Version: "0.8.0", Source: app.SourceUv, UvTool: "ruff"},
			body:    `not json`,
			status:  200,
			wantErr: true,
		},
		{
			name:    "PyPI returns empty version",
			app:     &app.App{Name: "ruff", Version: "0.8.0", Source: app.SourceUv, UvTool: "ruff"},
			body:    `{"info":{"version":""}}`,
			status:  200,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				fmt.Fprint(w, tt.body)
			}))
			defer ts.Close()

			c := NewUvChecker(ts.Client(), ts.URL)
			result, err := c.Check(context.Background(), tt.app)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.HasUpdate != tt.wantUpdate {
				t.Errorf("HasUpdate = %v, want %v", result.HasUpdate, tt.wantUpdate)
			}
			if result.LatestVersion != tt.wantLatest {
				t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, tt.wantLatest)
			}
			if result.Source != "uv" {
				t.Errorf("Source = %q, want %q", result.Source, "uv")
			}
		})
	}
}

func TestUvChecker_Check_CachesResults(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `{"info":{"version":"1.2.3"}}`)
	}))
	defer ts.Close()

	c := NewUvChecker(ts.Client(), ts.URL)
	a := &app.App{Name: "ruff", Version: "1.0.0", Source: app.SourceUv, UvTool: "ruff"}

	for i := 0; i < 3; i++ {
		if _, err := c.Check(context.Background(), a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("PyPI hits = %d, want 1 (results should be cached)", calls)
	}
}

func TestListInstalledUvTools(t *testing.T) {
	tests := []struct {
		name    string
		output  []byte
		err     error
		want    map[string]string
		wantErr bool
	}{
		{
			name: "normal output",
			output: []byte(`black v25.1.0
- black
- blackd
ruff v0.8.4
- ruff
huggingface-hub v1.14.0
- hf
- huggingface-cli
`),
			want: map[string]string{
				"black":           "25.1.0",
				"ruff":            "0.8.4",
				"huggingface-hub": "1.14.0",
			},
		},
		{
			name:   "no tools installed",
			output: []byte("No tools installed.\n"),
			want:   map[string]string{},
		},
		{
			name: "warning lines are skipped",
			output: []byte(`warning: Tool ` + "`kimi-cli`" + ` environment not found
black v25.1.0
- black
`),
			want: map[string]string{"black": "25.1.0"},
		},
		{
			name: "single-letter pep440 version",
			output: []byte(`llm v0.31
- llm
`),
			want: map[string]string{"llm": "0.31"},
		},
		{
			name: "name containing v",
			output: []byte(`openviking v0.3.16
- ov
`),
			want: map[string]string{"openviking": "0.3.16"},
		},
		{
			name:    "uv command fails",
			err:     fmt.Errorf("uv not found"),
			wantErr: true,
		},
		{
			name:   "empty output",
			output: []byte(""),
			want:   map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &MockCmdRunner{Output: tt.output, Err: tt.err}
			got, err := ListInstalledUvTools(context.Background(), runner)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d tools (%v), want %d (%v)", len(got), got, len(tt.want), tt.want)
			}
			for name, wantVer := range tt.want {
				if gotVer, ok := got[name]; !ok {
					t.Errorf("missing tool %q", name)
				} else if gotVer != wantVer {
					t.Errorf("tool %q version = %q, want %q", name, gotVer, wantVer)
				}
			}
		})
	}
}

func TestParseUvToolLine(t *testing.T) {
	tests := []struct {
		line     string
		wantName string
		wantVer  string
		wantOK   bool
	}{
		{"black v25.1.0", "black", "25.1.0", true},
		{"huggingface-hub v1.14.0", "huggingface-hub", "1.14.0", true},
		{"openviking v0.3.16", "openviking", "0.3.16", true},
		{"llm v0.31", "llm", "0.31", true},
		{"no-version-here", "", "", false},
		{"", "", "", false},
		{"weird vNotAVersion", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			name, ver, ok := parseUvToolLine(tt.line)
			if ok != tt.wantOK || name != tt.wantName || ver != tt.wantVer {
				t.Errorf("parseUvToolLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.line, name, ver, ok, tt.wantName, tt.wantVer, tt.wantOK)
			}
		})
	}
}

func TestUvToolFromNonRegistrySource(t *testing.T) {
	gitReceipt := `[tool]
requirements = [
    { name = "agent-reach", git = "https://github.com/Panniantong/Agent-Reach.git?rev=22d7f03" },
    { name = "browser-cookie3" },
]
entrypoints = [
    { name = "agent-reach", install-path = "/Users/x/.local/bin/agent-reach", from = "agent-reach" },
]
`
	registryReceipt := `[tool]
requirements = [{ name = "black" }]
python = "3.13.12"
entrypoints = [
    { name = "black", install-path = "/Users/x/.local/bin/black", from = "black" },
]
`
	pinnedReceipt := `[tool]
requirements = [
    { name = "bilibili-cli", specifier = "==0.6.2" },
    { name = "av", specifier = ">=14.0" },
]
`

	tests := []struct {
		name    string
		receipt string
		tool    string
		want    UvReceiptInfo
	}{
		{"git install is non-registry", gitReceipt, "agent-reach", UvReceiptInfo{NonRegistry: true}},
		{"plain registry install", registryReceipt, "black", UvReceiptInfo{}},
		{"pinned registry install", pinnedReceipt, "bilibili-cli", UvReceiptInfo{Pinned: true}},
		{"range specifier is not a pin", pinnedReceipt, "av", UvReceiptInfo{}},
		{"tool missing from receipt", registryReceipt, "ruff", UvReceiptInfo{}},
		{"underscore name matches dash entry", gitReceipt, "agent_reach", UvReceiptInfo{NonRegistry: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseUvReceipt(tt.receipt, tt.tool); got != tt.want {
				t.Errorf("parseUvReceipt(%q) = %+v, want %+v", tt.tool, got, tt.want)
			}
		})
	}
}

func TestUvChecker_Check_NonRegistrySkipsPyPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("PyPI should not be queried for non-registry tools")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewUvChecker(srv.Client(), srv.URL)
	a := &app.App{Name: "agent-reach", Version: "0.3.0", Source: app.SourceUv, UvTool: "agent-reach", UvNonRegistry: true}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if result.HasUpdate {
		t.Error("non-registry tool should not report an update")
	}
	if result.LatestVersion != "0.3.0" {
		t.Errorf("LatestVersion = %q, want installed version", result.LatestVersion)
	}
}
