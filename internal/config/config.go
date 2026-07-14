package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const defaultPort = 17329

type Config struct {
	DataDir string `json:"data_dir"`
	Port    int    `json:"port"`
}

func New(dataDir string) (*Config, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	return &Config{
		DataDir: dataDir,
		Port:    defaultPort,
	}, nil
}

func (c *Config) Load() error {
	path := filepath.Join(c.DataDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.Save()
		}
		return err
	}
	return json.Unmarshal(data, c)
}

func (c *Config) Save() error {
	path := filepath.Join(c.DataDir, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.DataDir, "config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
