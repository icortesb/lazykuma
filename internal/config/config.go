// Package config keeps the instances lazykuma knows and the tokens it logs in
// with, in two files: the instances are safe to keep in dotfiles, the tokens
// are not.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// Instance is one Kuma: a name to show and the URL of its web UI.
type Instance struct {
	Name string `toml:"name"`
	URL  string `toml:"url"`
}

// Config is the instances, in the order the menu lists them.
type Config struct {
	Instances []Instance `toml:"instance"`
}

// Paths are the config and token files, under XDG_CONFIG_HOME and
// XDG_STATE_HOME when set, ~/.config and ~/.local/state otherwise.
func Paths() (configFile, tokenFile string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	st := os.Getenv("XDG_STATE_HOME")
	if st == "" {
		st = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(cfg, "lazykuma", "config.toml"), filepath.Join(st, "lazykuma", "tokens.json"), nil
}

// Load reads the config. A missing file is an empty config. What cannot be
// used is skipped and explained in warnings rather than failing: lazykuma
// starts with whatever it could read.
func Load(path string) (Config, []string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil, nil
	}
	if err != nil {
		return Config{}, nil, err
	}

	var raw Config
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return Config{}, []string{fmt.Sprintf("%s: %v; starting with no instances", filepath.Base(path), err)}, nil
	}

	var c Config
	var warnings []string
	for i, in := range raw.Instances {
		in.Name, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL)
		if err := c.Add(in); err != nil {
			warnings = append(warnings, fmt.Sprintf("instance %d skipped: %v", i+1, err))
		}
	}
	return c, warnings, nil
}

// Add appends an instance after checking it.
func (c *Config) Add(in Instance) error {
	if in.Name == "" {
		return errors.New("the name is empty")
	}
	if err := ValidURL(in.URL); err != nil {
		return err
	}
	for _, have := range c.Instances {
		if strings.EqualFold(have.Name, in.Name) {
			return fmt.Errorf("there is already an instance called %q", have.Name)
		}
	}
	c.Instances = append(c.Instances, in)
	return nil
}

// ValidURL accepts the http or https address of a Kuma web UI.
func ValidURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL like https://kuma.example.com", s)
	}
	return nil
}

// header is the comment Save and AppendInstance start a fresh file with.
const header = "# lazykuma instances. Tokens live elsewhere; this file is safe to share.\n\n"

// Save writes the config.
func Save(path string, c Config) error {
	var b bytes.Buffer
	b.WriteString(header)
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return writeAtomic(path, b.Bytes(), 0o644)
}

// AppendInstance adds one instance to the config file on disk, leaving
// everything else in it alone: comments and entries Load could not use
// (Save, which re-encodes only what Load accepted, would silently drop
// them). A missing file starts from the same header Save writes.
func AppendInstance(path string, in Instance) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data = []byte(header)
	} else if err != nil {
		return err
	}

	var b bytes.Buffer
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if err := toml.NewEncoder(&b).Encode(Config{Instances: []Instance{in}}); err != nil {
		return err
	}

	out := b.Bytes()
	var check Config
	if _, err := toml.Decode(string(out), &check); err != nil {
		return fmt.Errorf("%s has a syntax error to fix before adding instances: %w", path, err)
	}
	return writeAtomic(path, out, 0o644)
}

// tokenEntry is one instance's stored login, keyed by the URL it was issued
// for: a token belongs to a host, not a name, so it must not follow a name
// to a different URL.
type tokenEntry struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Tokens are the login tokens by instance name, in a file only the user can
// read. Passwords are never stored.
type Tokens struct {
	path string
	mu   sync.Mutex
	m    map[string]tokenEntry
}

// LoadTokens reads the token file; a missing one holds no tokens. An entry
// in the old bare-string format (name to token, with no URL to check) is
// ignored rather than failing the load: there are no released users, so
// there is nothing to migrate, only a crash to avoid.
func LoadTokens(path string) (*Tokens, error) {
	t := &Tokens{path: path, m: map[string]tokenEntry{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, msg := range raw {
		var e tokenEntry
		if err := json.Unmarshal(msg, &e); err != nil {
			continue // old format: a bare string, no URL to trust it for
		}
		t.m[name] = e
	}
	return t, nil
}

// Get is the token of an instance, only when it was issued for url; a token
// left over from before a URL change is not returned, so it is never sent
// to a different host.
func (t *Tokens) Get(name, url string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[name]
	if e.URL != url {
		return ""
	}
	return e.Token
}

// Set stores an instance's token for url and writes the file.
func (t *Tokens) Set(name, url, token string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[name] = tokenEntry{URL: url, Token: token}
	data, err := json.MarshalIndent(t.m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(t.path, append(data, '\n'), 0o600)
}

// writeAtomic writes through a temporary file and a rename, so a crash never
// leaves half a file behind. When path is a symlink (a dotfiles setup), the
// temp file and the rename happen at its target, so the link itself is left
// alone. A missing path is not an error: it is the first write.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), target)
}
