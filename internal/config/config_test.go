package config

import (
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.TandoorURL != "http://tandoor.tandoor.svc.cluster.local" || c.TandoorPublicURL != c.TandoorURL {
		t.Errorf("urls = %q %q", c.TandoorURL, c.TandoorPublicURL)
	}
	if c.TandoorAIProviderID != 34 || c.TandoorTimeout != 120*time.Second || !c.AllowTikTok || len(c.Users) != 0 {
		t.Errorf("got %+v", c)
	}
	if c.MaxDownloadBytes != 100<<20 || c.WhisperThreads != 4 || c.WhisperBeamSize != 1 {
		t.Errorf("got %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"TANDOOR_URL":        "https://recipes.example.com/",
		"TANDOOR_PUBLIC_URL": "https://public.example.com/",
		"ALLOW_TIKTOK":       "false",
		"WHISPER_BEAM_SIZE":  "5",
		"REEL_USERS":         `{"k1": {"name": "mum", "tandoor_token": "tda_1"}}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.TandoorURL != "https://recipes.example.com" || c.TandoorPublicURL != "https://public.example.com" {
		t.Errorf("urls = %q %q", c.TandoorURL, c.TandoorPublicURL)
	}
	if c.AllowTikTok || c.WhisperBeamSize != 5 || c.Users["k1"] != (User{Name: "mum", TandoorToken: "tda_1"}) {
		t.Errorf("got %+v", c)
	}
}

func TestInvalidValues(t *testing.T) {
	_, err := Load(env(map[string]string{
		"TANDOOR_AI_PROVIDER_ID": "abc",
		"ALLOW_TIKTOK":           "maybe",
		"REEL_USERS":             `{"k1": {"name": "mum"}}`,
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"TANDOOR_AI_PROVIDER_ID", "ALLOW_TIKTOK", `entry "mum"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't mention %s", err, want)
		}
	}
	if _, err := Load(env(map[string]string{"REEL_USERS": "not json"})); err == nil {
		t.Error("bad REEL_USERS JSON accepted")
	}
}
