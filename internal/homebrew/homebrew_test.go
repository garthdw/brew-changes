package homebrew

import (
	"encoding/json"
	"testing"
)

func TestFirstOrEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty slice", nil, ""},
		{"single element", []string{"1.2.3"}, "1.2.3"},
		{"multiple elements returns first", []string{"1.2.3", "1.2.2"}, "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstOrEmpty(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutdatedResult_Unmarshal(t *testing.T) {
	raw := `{
		"formulae": [
			{"name": "git", "installed_versions": ["2.40.0"], "current_version": "2.41.0"}
		],
		"casks": []
	}`

	var result outdatedResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(result.Formulae) != 1 {
		t.Fatalf("got %d formulae, want 1", len(result.Formulae))
	}
	f := result.Formulae[0]
	if f.Name != "git" || f.CurrentVersion != "2.41.0" || firstOrEmpty(f.InstalledVersions) != "2.40.0" {
		t.Errorf("got %+v", f)
	}
}

func TestFormulaInfo_Unmarshal_PrefersStableURL(t *testing.T) {
	raw := `{
		"formulae": [
			{
				"name": "opencode",
				"full_name": "anomalyco/tap/opencode",
				"homepage": "https://example.com",
				"urls": {
					"stable": {"url": "https://example.com/stable.tar.gz"},
					"head": {"url": "https://example.com/head.tar.gz"}
				}
			}
		]
	}`

	var result infoResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(result.Formulae) != 1 {
		t.Fatalf("got %d formulae, want 1", len(result.Formulae))
	}
	f := result.Formulae[0]
	if f.FullName != "anomalyco/tap/opencode" {
		t.Errorf("got FullName %q, want tap-qualified full_name", f.FullName)
	}
	url := f.URLs.Stable.URL
	if url == "" {
		url = f.URLs.Head.URL
	}
	if url != "https://example.com/stable.tar.gz" {
		t.Errorf("got %q, want stable url", url)
	}
}

func TestCaskInfo_Unmarshal(t *testing.T) {
	raw := `{
		"casks": [
			{"token": "app", "full_token": "someuser/tap/app", "homepage": "https://example.com", "url": "https://example.com/app.dmg"}
		]
	}`

	var result infoResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(result.Casks) != 1 {
		t.Fatalf("got %d casks, want 1", len(result.Casks))
	}
	c := result.Casks[0]
	if c.FullToken != "someuser/tap/app" {
		t.Errorf("got FullToken %q, want tap-qualified full_token", c.FullToken)
	}
	if c.Homepage != "https://example.com" || c.URL != "https://example.com/app.dmg" {
		t.Errorf("got %+v", c)
	}
}
