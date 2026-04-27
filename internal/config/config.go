package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "/etc/openclaw-agent/config.yaml"
)

type Config struct {
	InstanceID                   string        `yaml:"instance_id"`
	BootstrapToken               string        `yaml:"bootstrap_token"`
	ControlPlaneBaseURL          string        `yaml:"control_plane_base_url"`
	AgentDataDir                 string        `yaml:"agent_data_dir"`
	DiskUsagePath                string        `yaml:"disk_usage_path"`
	DiskLimitBytes               uint64        `yaml:"disk_limit_bytes"`
	InitialConfigRevisionID      string        `yaml:"initial_config_revision_id"`
	ProtocolVersion              string        `yaml:"protocol_version"`
	LocalHTTPBind                string        `yaml:"local_http_bind"`
	LogFilePath                  string        `yaml:"log_file_path"`
	OpenClawCommand              []string      `yaml:"openclaw_command"`
	OpenClawConfigPath           string        `yaml:"openclaw_config_path"`
	OpenClawWorkspacePath        string        `yaml:"openclaw_workspace_path"`
	OpenClawSkillsPath           string        `yaml:"openclaw_skills_path"`
	OpenClawBuiltinSkillsPath    string        `yaml:"openclaw_builtin_skills_path"`
	OpenClawHealthURL            string        `yaml:"openclaw_health_url"`
	OpenClawDefaultsDir          string        `yaml:"openclaw_defaults_dir"`
	AutostartDefaultsDir         string        `yaml:"autostart_defaults_dir"`
	AutostartTargetDir           string        `yaml:"autostart_target_dir"`
	OpenClawExtensionsDir        string        `yaml:"openclaw_extensions_dir"`
	OpenClawBundledExtensionsDir string        `yaml:"openclaw_bundled_extensions_dir"`
	InstalledPluginPathPrefix    string        `yaml:"installed_plugin_path_prefix"`
	DropUserName                 string        `yaml:"drop_user_name"`
	HeartbeatInterval            time.Duration `yaml:"-"`
	StateReportInterval          time.Duration `yaml:"-"`
	CommandPollInterval          time.Duration `yaml:"-"`
	CommandPollBackoffMax        time.Duration `yaml:"-"`
	RegisterRetryInterval        time.Duration `yaml:"-"`
	ProcessStopTimeout           time.Duration `yaml:"-"`
	SkillIncrementalInterval     time.Duration `yaml:"-"`
	SkillFullSyncInterval        time.Duration `yaml:"-"`
	MaxAutoRestart               int           `yaml:"max_auto_restart"`
	HeartbeatIntervalRaw         string        `yaml:"heartbeat_interval"`
	StateReportIntervalRaw       string        `yaml:"state_report_interval"`
	CommandPollIntervalRaw       string        `yaml:"command_poll_interval"`
	CommandPollBackoffMaxRaw     string        `yaml:"command_poll_backoff_max"`
	RegisterRetryIntervalRaw     string        `yaml:"register_retry_interval"`
	ProcessStopTimeoutRaw        string        `yaml:"process_stop_timeout"`
	SkillIncrementalRaw          string        `yaml:"skill_incremental_interval"`
	SkillFullSyncRaw             string        `yaml:"skill_full_sync_interval"`
}

func Load() (Config, error) {
	cfg := Config{
		AgentDataDir:                 "/var/lib/openclaw-agent",
		DiskUsagePath:                "/config",
		ProtocolVersion:              "v1",
		LocalHTTPBind:                "0.0.0.0:18080",
		LogFilePath:                  "/var/log/openclaw-agent/agent.log",
		OpenClawCommand:              []string{"openclaw", "gateway", "run"},
		OpenClawConfigPath:           "/config/.openclaw/openclaw.json",
		OpenClawWorkspacePath:        "/config/.openclaw/workspace",
		OpenClawSkillsPath:           "/config/.openclaw/workspace/skills",
		OpenClawBuiltinSkillsPath:    "/usr/lib/node_modules/openclaw/skills",
		OpenClawHealthURL:            "http://127.0.0.1:18789/health",
		OpenClawDefaultsDir:          "/defaults/.openclaw",
		AutostartDefaultsDir:         "/defaults/.config/autostart",
		AutostartTargetDir:           "/config/.config/autostart",
		OpenClawExtensionsDir:        "/config/.openclaw/extensions",
		OpenClawBundledExtensionsDir: "/usr/local/lib/node_modules/openclaw/dist/extensions",
		InstalledPluginPathPrefix:    "/defaults/.openclaw/extensions/",
		DropUserName:                 "abc",
		HeartbeatIntervalRaw:         "15s",
		StateReportIntervalRaw:       "45s",
		CommandPollIntervalRaw:       "5s",
		CommandPollBackoffMaxRaw:     "60s",
		RegisterRetryIntervalRaw:     "10s",
		ProcessStopTimeoutRaw:        "20s",
		SkillIncrementalRaw:          "30s",
		SkillFullSyncRaw:             "12h",
		MaxAutoRestart:               3,
	}

	path := envOrDefault("OPENCLAW_AGENT_CONFIG_PATH", defaultConfigPath)
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	overrideStringAny(&cfg.InstanceID, "OPENCLAW_AGENT_INSTANCE_ID", "CLAWMANAGER_AGENT_INSTANCE_ID", "INSTANCE_ID")
	overrideStringAny(&cfg.BootstrapToken, "OPENCLAW_AGENT_BOOTSTRAP_TOKEN", "CLAWMANAGER_AGENT_BOOTSTRAP_TOKEN")
	overrideStringAny(&cfg.ControlPlaneBaseURL, "OPENCLAW_AGENT_CONTROL_PLANE_BASE_URL", "CLAWMANAGER_AGENT_BASE_URL")
	overrideStringAny(&cfg.AgentDataDir, "OPENCLAW_AGENT_DATA_DIR")
	overrideStringAny(&cfg.DiskUsagePath, "OPENCLAW_AGENT_DISK_USAGE_PATH", "CLAWMANAGER_AGENT_DISK_USAGE_PATH", "CLAWMANAGER_AGENT_PERSISTENT_DIR")
	overrideStringAny(&cfg.InitialConfigRevisionID, "OPENCLAW_AGENT_INITIAL_CONFIG_REVISION_ID")
	overrideStringAny(&cfg.ProtocolVersion, "OPENCLAW_AGENT_PROTOCOL_VERSION", "CLAWMANAGER_AGENT_PROTOCOL_VERSION")
	overrideStringAny(&cfg.LocalHTTPBind, "OPENCLAW_AGENT_LOCAL_HTTP_BIND")
	overrideStringAny(&cfg.LogFilePath, "OPENCLAW_AGENT_LOG_FILE_PATH")
	overrideStringAny(&cfg.OpenClawConfigPath, "OPENCLAW_AGENT_OPENCLAW_CONFIG_PATH")
	overrideStringAny(&cfg.OpenClawWorkspacePath, "OPENCLAW_AGENT_OPENCLAW_WORKSPACE_PATH")
	overrideStringAny(&cfg.OpenClawSkillsPath, "OPENCLAW_AGENT_OPENCLAW_SKILLS_PATH")
	overrideStringAny(&cfg.OpenClawBuiltinSkillsPath, "OPENCLAW_AGENT_OPENCLAW_BUILTIN_SKILLS_PATH")
	overrideStringAny(&cfg.OpenClawHealthURL, "OPENCLAW_AGENT_OPENCLAW_HEALTH_URL")
	overrideStringAny(&cfg.OpenClawDefaultsDir, "OPENCLAW_AGENT_OPENCLAW_DEFAULTS_DIR")
	overrideStringAny(&cfg.AutostartDefaultsDir, "OPENCLAW_AGENT_AUTOSTART_DEFAULTS_DIR")
	overrideStringAny(&cfg.AutostartTargetDir, "OPENCLAW_AGENT_AUTOSTART_TARGET_DIR")
	overrideStringAny(&cfg.OpenClawExtensionsDir, "OPENCLAW_AGENT_OPENCLAW_EXTENSIONS_DIR")
	overrideStringAny(&cfg.OpenClawBundledExtensionsDir, "OPENCLAW_AGENT_OPENCLAW_BUNDLED_EXTENSIONS_DIR")
	overrideStringAny(&cfg.InstalledPluginPathPrefix, "OPENCLAW_AGENT_INSTALLED_PLUGIN_PATH_PREFIX")
	overrideStringAny(&cfg.DropUserName, "OPENCLAW_AGENT_DROP_USER_NAME")
	overrideStringAny(&cfg.HeartbeatIntervalRaw, "OPENCLAW_AGENT_HEARTBEAT_INTERVAL")
	overrideStringAny(&cfg.StateReportIntervalRaw, "OPENCLAW_AGENT_STATE_REPORT_INTERVAL")
	overrideStringAny(&cfg.CommandPollIntervalRaw, "OPENCLAW_AGENT_COMMAND_POLL_INTERVAL")
	overrideStringAny(&cfg.CommandPollBackoffMaxRaw, "OPENCLAW_AGENT_COMMAND_POLL_BACKOFF_MAX")
	overrideStringAny(&cfg.RegisterRetryIntervalRaw, "OPENCLAW_AGENT_REGISTER_RETRY_INTERVAL")
	overrideStringAny(&cfg.ProcessStopTimeoutRaw, "OPENCLAW_AGENT_PROCESS_STOP_TIMEOUT")
	overrideStringAny(&cfg.SkillIncrementalRaw, "OPENCLAW_AGENT_SKILL_INCREMENTAL_INTERVAL")
	overrideStringAny(&cfg.SkillFullSyncRaw, "OPENCLAW_AGENT_SKILL_FULL_SYNC_INTERVAL")

	if raw := envFirst("OPENCLAW_AGENT_OPENCLAW_COMMAND"); raw != "" {
		cfg.OpenClawCommand = strings.Fields(raw)
	}
	if raw := envFirst("OPENCLAW_AGENT_MAX_AUTO_RESTART"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parse OPENCLAW_AGENT_MAX_AUTO_RESTART: %w", err)
		}
		cfg.MaxAutoRestart = n
	}
	if raw := envFirst("OPENCLAW_AGENT_DISK_LIMIT_BYTES", "CLAWMANAGER_AGENT_DISK_LIMIT_BYTES"); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse disk limit bytes: %w", err)
		}
		cfg.DiskLimitBytes = n
	}

	var err error
	if cfg.HeartbeatInterval, err = time.ParseDuration(cfg.HeartbeatIntervalRaw); err != nil {
		return Config{}, fmt.Errorf("parse heartbeat_interval: %w", err)
	}
	if cfg.StateReportInterval, err = time.ParseDuration(cfg.StateReportIntervalRaw); err != nil {
		return Config{}, fmt.Errorf("parse state_report_interval: %w", err)
	}
	if cfg.CommandPollInterval, err = time.ParseDuration(cfg.CommandPollIntervalRaw); err != nil {
		return Config{}, fmt.Errorf("parse command_poll_interval: %w", err)
	}
	if cfg.CommandPollBackoffMax, err = time.ParseDuration(cfg.CommandPollBackoffMaxRaw); err != nil {
		return Config{}, fmt.Errorf("parse command_poll_backoff_max: %w", err)
	}
	if cfg.RegisterRetryInterval, err = time.ParseDuration(cfg.RegisterRetryIntervalRaw); err != nil {
		return Config{}, fmt.Errorf("parse register_retry_interval: %w", err)
	}
	if cfg.ProcessStopTimeout, err = time.ParseDuration(cfg.ProcessStopTimeoutRaw); err != nil {
		return Config{}, fmt.Errorf("parse process_stop_timeout: %w", err)
	}
	if cfg.SkillIncrementalInterval, err = time.ParseDuration(cfg.SkillIncrementalRaw); err != nil {
		return Config{}, fmt.Errorf("parse skill_incremental_interval: %w", err)
	}
	if cfg.SkillFullSyncInterval, err = time.ParseDuration(cfg.SkillFullSyncRaw); err != nil {
		return Config{}, fmt.Errorf("parse skill_full_sync_interval: %w", err)
	}

	if cfg.InstanceID == "" {
		return Config{}, errors.New("instance_id is required")
	}
	if cfg.BootstrapToken == "" {
		return Config{}, errors.New("bootstrap_token is required")
	}
	if cfg.ControlPlaneBaseURL == "" {
		return Config{}, errors.New("control_plane_base_url is required")
	}
	if len(cfg.OpenClawCommand) == 0 {
		return Config{}, errors.New("openclaw_command is required")
	}

	cfg.AgentDataDir = filepath.Clean(cfg.AgentDataDir)
	cfg.DiskUsagePath = filepath.Clean(cfg.DiskUsagePath)
	cfg.OpenClawConfigPath = filepath.Clean(cfg.OpenClawConfigPath)
	cfg.OpenClawWorkspacePath = filepath.Clean(cfg.OpenClawWorkspacePath)
	cfg.OpenClawSkillsPath = filepath.Clean(cfg.OpenClawSkillsPath)
	cfg.OpenClawBuiltinSkillsPath = filepath.Clean(cfg.OpenClawBuiltinSkillsPath)
	cfg.OpenClawDefaultsDir = filepath.Clean(cfg.OpenClawDefaultsDir)
	cfg.AutostartDefaultsDir = filepath.Clean(cfg.AutostartDefaultsDir)
	cfg.AutostartTargetDir = filepath.Clean(cfg.AutostartTargetDir)
	cfg.OpenClawExtensionsDir = filepath.Clean(cfg.OpenClawExtensionsDir)
	cfg.OpenClawBundledExtensionsDir = filepath.Clean(cfg.OpenClawBundledExtensionsDir)
	return cfg, nil
}

func overrideString(target *string, envKey string) {
	if value := os.Getenv(envKey); value != "" {
		*target = value
	}
}

func overrideStringAny(target *string, envKeys ...string) {
	if value := envFirst(envKeys...); value != "" {
		*target = value
	}
}

func envFirst(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
