package openclawinspector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Inspector struct {
	configPath        string
	workspacePath     string
	builtinSkillsPath string
}

type Snapshot struct {
	Version      string         `json:"version,omitempty"`
	ConfigPath   string         `json:"config_path"`
	Workspace    string         `json:"workspace_path"`
	Stats        map[string]any `json:"stats"`
	CollectedAt  time.Time      `json:"collected_at"`
	ConfigExists bool           `json:"config_exists"`
}

func New(configPath string, workspacePath string, builtinSkillsPath string) *Inspector {
	return &Inspector{
		configPath:        configPath,
		workspacePath:     workspacePath,
		builtinSkillsPath: builtinSkillsPath,
	}
}

func (i *Inspector) Collect() Snapshot {
	stats := map[string]any{
		"skill_count":   0,
		"agent_count":   0,
		"channel_count": 0,
	}
	snapshot := Snapshot{
		ConfigPath:  i.configPath,
		Workspace:   i.workspacePath,
		Stats:       stats,
		CollectedAt: time.Now().UTC(),
	}

	raw, err := os.ReadFile(i.configPath)
	if err == nil {
		snapshot.ConfigExists = true
		var cfg map[string]any
		if json.Unmarshal(raw, &cfg) == nil {
			stats["channel_count"] = countObjectKeys(cfg["channels"])
			stats["agent_count"] = countNamedAgents(cfg["agents"])
			if version := nestedString(cfg, "meta", "lastTouchedVersion"); version != "" {
				snapshot.Version = version
			}
		}
	}

	if snapshot.Version == "" {
		snapshot.Version = i.detectVersion()
	}
	stats["skill_count"] = i.countSkills()

	return snapshot
}

func (i *Inspector) detectVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "openclaw", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (i *Inspector) countSkills() int {
	return countSkillEntries(filepath.Join(i.workspacePath, "skills")) + countSkillEntries(i.builtinSkillsPath)
}

func countSkillEntries(root string) int {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".disabled") {
			continue
		}
		if entry.IsDir() {
			count++
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".md") {
			count++
		}
	}
	return count
}

func countObjectKeys(value any) int {
	obj, ok := value.(map[string]any)
	if !ok {
		return 0
	}
	return len(obj)
}

func countNamedAgents(value any) int {
	obj, ok := value.(map[string]any)
	if !ok {
		return 0
	}
	count := 0
	for key := range obj {
		if key == "defaults" {
			continue
		}
		count++
	}
	return count
}

func nestedString(root map[string]any, path ...string) string {
	current := any(root)
	for _, part := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		next, ok := obj[part]
		if !ok {
			return ""
		}
		current = next
	}
	value, ok := current.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (i *Inspector) Validate() error {
	if i.configPath == "" {
		return errors.New("openclaw config path is required")
	}
	if i.workspacePath == "" {
		return errors.New("openclaw workspace path is required")
	}
	return nil
}

func (i *Inspector) String() string {
	return fmt.Sprintf("config=%s workspace=%s", i.configPath, i.workspacePath)
}
