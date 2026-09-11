package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsFollowXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	t.Setenv("XDG_STATE_HOME", "/x/state")
	cfg, tok, err := Paths()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != "/x/cfg/lazykuma/config.toml" || tok != "/x/state/lazykuma/tokens.json" {
		t.Fatalf("got %s, %s", cfg, tok)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	cfg, tok, _ = Paths()
	if cfg != "/home/u/.config/lazykuma/config.toml" || tok != "/home/u/.local/state/lazykuma/tokens.json" {
		t.Fatalf("defaults: %s, %s", cfg, tok)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	c, warns, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || len(c.Instances) != 0 || len(warns) != 0 {
		t.Fatalf("got %+v %v %v", c, warns, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	var c Config
	if err := c.Add(Instance{Name: "home", URL: "http://kuma.lan:3001"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(Instance{Name: "vps", URL: "https://status.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, warns, err := Load(path)
	if err != nil || len(warns) != 0 {
		t.Fatal(err, warns)
	}
	if len(got.Instances) != 2 || got.Instances[0] != c.Instances[0] || got.Instances[1] != c.Instances[1] {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadSkipsBadEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
[[instance]]
name = "home"
url = "http://kuma.lan"

[[instance]]
name = ""
url = "http://x.lan"

[[instance]]
name = "home"
url = "http://other.lan"

[[instance]]
name = "vps"
url = "status.example.com"
`), 0o644)
	c, warns, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Instances) != 1 || c.Instances[0].Name != "home" {
		t.Fatalf("instances = %+v", c.Instances)
	}
	if len(warns) != 3 || !strings.HasPrefix(warns[0], "instance 2 skipped") {
		t.Fatalf("warnings = %q", warns)
	}
}

func TestLoadBrokenFileWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("[[instance]\nname = "), 0o644)
	c, warns, err := Load(path)
	if err != nil || len(c.Instances) != 0 || len(warns) != 1 {
		t.Fatalf("got %+v %q %v", c, warns, err)
	}
}

// TestAppendInstanceKeepsExistingContent is the fix for F1: appending must
// not re-encode the file through Load's lenient decoder, or a comment and an
// entry Load skips (kept on disk for the user to fix by hand) would vanish.
func TestAppendInstanceKeepsExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# hand-edited, in dotfiles\n\n[[instance]]\nname = \"\"\nurl = \"http://bad.lan\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendInstance(path, Instance{Name: "home", URL: "http://kuma.lan"}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), original) {
		t.Fatalf("existing content changed:\n%s", got)
	}

	c, warns, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want the empty-name entry still skipped", warns)
	}
	if len(c.Instances) != 1 || c.Instances[0] != (Instance{Name: "home", URL: "http://kuma.lan"}) {
		t.Fatalf("instances = %+v", c.Instances)
	}
}

func TestAppendInstanceSyntaxErrorLeavesFileUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	broken := "[[instance]\nname = "
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendInstance(path, Instance{Name: "home", URL: "http://kuma.lan"}); err == nil {
		t.Fatal("want an error for a broken config file")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != broken {
		t.Fatalf("file changed: %q, %v", got, err)
	}
}

func TestAppendInstanceThroughSymlinkStaysASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real", "config.toml")
	if err := Save(target, Config{}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := AppendInstance(link, Instance{Name: "home", URL: "http://kuma.lan"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the config path is no longer a symlink")
	}
	linkTarget, err := os.Readlink(link)
	if err != nil || linkTarget != target {
		t.Fatalf("symlink now points at %q, %v", linkTarget, err)
	}

	c, warns, err := Load(target)
	if err != nil || len(warns) != 0 || len(c.Instances) != 1 || c.Instances[0].Name != "home" {
		t.Fatalf("target = %+v %v %v", c, warns, err)
	}
}

func TestAppendInstanceCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := AppendInstance(path, Instance{Name: "home", URL: "http://kuma.lan"}); err != nil {
		t.Fatal(err)
	}
	c, warns, err := Load(path)
	if err != nil || len(warns) != 0 || len(c.Instances) != 1 || c.Instances[0].Name != "home" {
		t.Fatalf("loaded = %+v %v %v", c, warns, err)
	}
}

func TestAddRejects(t *testing.T) {
	c := Config{Instances: []Instance{{Name: "Home", URL: "http://a.lan"}}}
	for _, in := range []Instance{
		{Name: "", URL: "http://b.lan"},
		{Name: "home", URL: "http://b.lan"}, // names are case-insensitive
		{Name: "b", URL: "b.lan"},
		{Name: "b", URL: "ftp://b.lan"},
	} {
		if err := c.Add(in); err == nil {
			t.Errorf("Add(%+v) = nil", in)
		}
	}
}

func TestTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "tokens.json")
	tok, err := LoadTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Get("home") != "" {
		t.Fatal("token out of nowhere")
	}
	if err := tok.Set("home", "jwt-1"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode %o, want 600", perm)
	}

	again, err := LoadTokens(path)
	if err != nil || again.Get("home") != "jwt-1" {
		t.Fatalf("reloaded = %q, %v", again.Get("home"), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("left temporary files: %v", entries)
	}
}
