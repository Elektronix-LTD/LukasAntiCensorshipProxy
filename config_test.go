package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfigListenAddr(t *testing.T) {
	c := defaultConfig()
	if c.ListenAddr() != "127.0.0.1:45777" {
		t.Fatalf("ListenAddr = %q", c.ListenAddr())
	}
}

func TestApplyFileConfigPartial(t *testing.T) {
	cfg := defaultConfig()
	port := 9999
	if err := applyFileConfig(&cfg, fileConfig{Port: &port}); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("host should stay default, got %q", cfg.Host)
	}
	if cfg.Timeout != 15*time.Second {
		t.Fatalf("timeout should stay default, got %s", cfg.Timeout)
	}
}

func TestApplyFileConfigDurationsAndQuiet(t *testing.T) {
	cfg := defaultConfig()
	host := "::1"
	timeout := "30s"
	gap := "25ms"
	quiet := true
	if err := applyFileConfig(&cfg, fileConfig{
		Host:    &host,
		Timeout: &timeout,
		Gap:     &gap,
		Quiet:   &quiet,
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "::1" {
		t.Fatalf("host = %q", cfg.Host)
	}
	if cfg.ListenAddr() != "[::1]:45777" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr())
	}
	if cfg.Timeout != 30*time.Second || cfg.Gap != 25*time.Millisecond || !cfg.Quiet {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestValidatePort(t *testing.T) {
	cfg := defaultConfig()
	cfg.Port = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for port 0")
	}
	cfg.Port = 65536
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for port 65536")
	}
	cfg.Port = 443
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMergeConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lacp.json")
	body := []byte(`{"host":"127.0.0.1","port":7777,"timeout":"5s","gap":"1ms","quiet":true}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, used, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if used != path {
		t.Fatalf("used = %q", used)
	}
	if cfg.Port != 7777 || cfg.Timeout != 5*time.Second || cfg.Gap != time.Millisecond || !cfg.Quiet {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadConfigMissingExplicitFile(t *testing.T) {
	_, _, err := loadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing -config path")
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lacp.json")
	if err := os.WriteFile(path, []byte(`{port: 8}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestIsGoBuildCache(t *testing.T) {
	yes := []string{
		`C:\Users\Lukas\AppData\Local\Temp\go-build123\b001`,
		`/tmp/go-build123/b001`,
		filepath.Join(os.TempDir(), "go-build123", "b001"),
	}
	for _, dir := range yes {
		if !isGoBuildCache(dir) {
			t.Fatalf("expected go-build path to be detected: %q", dir)
		}
	}
	no := []string{
		`C:\Users\Lukas\sni-strip-proxy`,
		`/home/runner/work/lacp/lacp`,
		filepath.Join(os.TempDir(), "lacp"),
	}
	for _, dir := range no {
		if isGoBuildCache(dir) {
			t.Fatalf("project dir is not a go-build cache: %q", dir)
		}
	}
}
