package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultAgent string                  `yaml:"default_agent"`
	Providers    map[string]ProviderConf `yaml:"providers"`
}

type ProviderConf struct {
	Type    string   `yaml:"type"`     // "openai" (default) or "anthropic"
	APIKey  string   `yaml:"api_key"`
	BaseURL string   `yaml:"base_url"`
	Models  []string `yaml:"models"`   // available models for this provider
}

type MCPConf struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Timeout int               `yaml:"timeout"` // seconds, default 30
}

type AgentConf struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	SystemPrompt string   `yaml:"system_prompt"`
	Models       []string `yaml:"models"`
	DefaultModel string   `yaml:"default_model"`
	Tools        []string `yaml:"tools"`
	Skills       []string              `yaml:"skills"`
	MCPs         map[string]MCPConf    `yaml:"mcps"`
}

func GalDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gal")
}

func Load() (*Config, error) {
	data, err := os.ReadFile(filepath.Join(GalDir(), "gal.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	data = []byte(os.ExpandEnv(string(data)))
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func LoadAgent(name string) (*AgentConf, error) {
	path := filepath.Join(GalDir(), "agents", name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load agent %s: %w", name, err)
	}
	data = []byte(os.ExpandEnv(string(data)))
	var agent AgentConf
	if err := yaml.Unmarshal(data, &agent); err != nil {
		return nil, fmt.Errorf("parse agent %s: %w", name, err)
	}
	return &agent, nil
}

func ListAgents() ([]string, error) {
	dir := filepath.Join(GalDir(), "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
		}
	}
	return names, nil
}
