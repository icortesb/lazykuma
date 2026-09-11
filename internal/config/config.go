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

// Save writes the config.
func Save(path string, c Config) error {
	var b bytes.Buffer
	b.WriteString("# lazykuma instances. Tokens live elsewhere; this file is safe to share.\n\n")
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return writeAtomic(path, b.Bytes(), 0o644)
}

// Tokens are the login tokens by instance name, in a file only the user can
// read. Passwords are never stored.
type Tokens struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

// LoadTokens reads the token file; a missing one holds no tokens.
func LoadTokens(path string) (*Tokens, error) {
	t := &Tokens{path: path, m: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &t.m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// Get is the token of an instance, or "".
func (t *Tokens) Get(name string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[name]
}

// Set stores an instance's token and writes the file.
func (t *Tokens) Set(name, token string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[name] = token
	data, err := json.MarshalIndent(t.m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(t.path, append(data, '\n'), 0o600)
}

// writeAtomic writes through a temporary file and a rename, so a crash never
// leaves half a file behind.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
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
	return os.Rename(f.Name(), path)
}
