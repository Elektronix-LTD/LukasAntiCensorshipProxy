package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const configFileName = "lacp.json"

// Config is the runtime settings for Lukas-Anti-Censorship-Proxy.
type Config struct {
	Host    string
	Port    int
	Timeout time.Duration
	Gap     time.Duration
	Quiet   bool
}

// fileConfig uses pointers so a JSON file may omit keys; omitted fields
// keep the built-in defaults instead of being zeroed.
type fileConfig struct {
	Host    *string `json:"host"`
	Port    *int    `json:"port"`
	Timeout *string `json:"timeout"`
	Gap     *string `json:"gap"`
	Quiet   *bool   `json:"quiet"`
}

func defaultConfig() Config {
	return Config{
		Host:    "127.0.0.1",
		Port:    45777,
		Timeout: 15 * time.Second,
		Gap:     10 * time.Millisecond,
		Quiet:   false,
	}
}

func defaultConfigJSON() []byte {
	c := defaultConfig()
	raw, _ := json.MarshalIndent(fileConfig{
		Host:    strPtr(c.Host),
		Port:    intPtr(c.Port),
		Timeout: strPtr(c.Timeout.String()),
		Gap:     strPtr(c.Gap.String()),
		Quiet:   boolPtr(c.Quiet),
	}, "", "  ")
	return append(raw, '\n')
}

// ListenAddr is host:port, with brackets around IPv6 literals.
func (c Config) ListenAddr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("host must not be empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be 1–65535, got %d", c.Port)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %s", c.Timeout)
	}
	if c.Gap < 0 {
		return fmt.Errorf("gap must be >= 0, got %s", c.Gap)
	}
	return nil
}

// loadConfig resolves the JSON file, creates one with defaults if needed,
// and returns the merged settings plus the path that was used (empty when
// running purely on built-in defaults).
func loadConfig(explicitPath string) (Config, string, error) {
	cfg := defaultConfig()

	if explicitPath != "" {
		if err := mergeConfigFile(&cfg, explicitPath); err != nil {
			return Config{}, explicitPath, err
		}
		return cfg, explicitPath, cfg.validate()
	}

	path, created, err := findOrCreateConfig()
	if err != nil {
		return Config{}, "", err
	}
	if path == "" {
		return cfg, "", cfg.validate()
	}
	if !created {
		if err := mergeConfigFile(&cfg, path); err != nil {
			return Config{}, path, err
		}
	}
	return cfg, path, cfg.validate()
}

func mergeConfigFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var raw fileConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return applyFileConfig(cfg, raw)
}

func applyFileConfig(cfg *Config, raw fileConfig) error {
	if raw.Host != nil {
		cfg.Host = *raw.Host
	}
	if raw.Port != nil {
		cfg.Port = *raw.Port
	}
	if raw.Timeout != nil {
		d, err := time.ParseDuration(*raw.Timeout)
		if err != nil {
			return fmt.Errorf("timeout %q: %w", *raw.Timeout, err)
		}
		cfg.Timeout = d
	}
	if raw.Gap != nil {
		d, err := time.ParseDuration(*raw.Gap)
		if err != nil {
			return fmt.Errorf("gap %q: %w", *raw.Gap, err)
		}
		cfg.Gap = d
	}
	if raw.Quiet != nil {
		cfg.Quiet = *raw.Quiet
	}
	return nil
}

// findOrCreateConfig looks for lacp.json next to the executable, then in
// the working directory. If neither exists, it writes a default file next
// to the executable (or cwd if that directory is not writable / is a
// `go run` temp folder).
func findOrCreateConfig() (path string, created bool, err error) {
	for _, dir := range configSearchDirs() {
		p := filepath.Join(dir, configFileName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, false, nil
		}
	}

	for _, dir := range configSearchDirs() {
		p := filepath.Join(dir, configFileName)
		if err := os.WriteFile(p, defaultConfigJSON(), 0o644); err != nil {
			continue
		}
		return p, true, nil
	}
	return "", false, nil
}

func configSearchDirs() []string {
	seen := make(map[string]bool)
	var dirs []string
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}

	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		if !isGoBuildCache(dir) {
			add(dir)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	return dirs
}

// isGoBuildCache reports directories where `go run` drops the temporary
// binary. Writing lacp.json there would hide it from the user.
func isGoBuildCache(dir string) bool {
	slash := filepath.ToSlash(dir)
	return strings.Contains(slash, "/go-build")
}

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }
func boolPtr(b bool) *bool    { return &b }
