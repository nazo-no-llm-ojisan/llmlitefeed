package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNew_creates_directory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "not-exists")

	cfg, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q) returned error: %v", dir, err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("os.Stat(%q) returned error: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("expected directory %q, but is not a directory", dir)
	}
	if cfg.DataDir != dir {
		t.Errorf("cfg.DataDir = %q, want %q", cfg.DataDir, dir)
	}
}

func TestNew_then_Load_with_empty_dir_sets_defaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q) returned error: %v", dir, err)
	}

	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Port != 17329 {
		t.Errorf("cfg.Port = %d, want 17329", cfg.Port)
	}

	// config.json should exist
	configPath := filepath.Join(dir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected config.json to exist at %q, but got error: %v", configPath, err)
	}
}

func TestSave_then_Load_restores_values(t *testing.T) {
	dir := t.TempDir()

	// First instance: save custom values
	cfg1, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q) returned error: %v", dir, err)
	}
	cfg1.Port = 9999
	if err := cfg1.Save(); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	// Second instance: load and verify
	cfg2, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q) returned error: %v", dir, err)
	}
	if err := cfg2.Load(); err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg2.Port != 9999 {
		t.Errorf("cfg2.Port = %d, want 9999", cfg2.Port)
	}

	// Verify config.json contains the saved values
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) returned error: %v", configPath, err)
	}

	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Port != 9999 {
		t.Errorf("decoded.Port = %d, want 9999", decoded.Port)
	}
}
