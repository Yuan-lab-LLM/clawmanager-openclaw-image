package profiler

import (
	"bufio"
	"bytes"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	appconfig "github.com/iamlovingit/clawmanager-openclaw-image/internal/config"
)

type Profiler struct {
	diskUsagePath  string
	diskLimitBytes uint64
}

func New(cfg appconfig.Config) *Profiler {
	return &Profiler{
		diskUsagePath:  cfg.DiskUsagePath,
		diskLimitBytes: cfg.DiskLimitBytes,
	}
}

func (p *Profiler) Collect() map[string]any {
	hostname, _ := os.Hostname()
	kernel := readTrimmed("/proc/sys/kernel/osrelease")
	osVersion := detectOSVersion()
	hostMemTotalKB, hostMemAvailableKB := readHostMemInfo()
	load1, load5, load15 := readLoadAvg()
	cpuInfo := collectCPUInfo(load1, load5, load15)
	memoryInfo := collectMemoryInfo(hostMemTotalKB, hostMemAvailableKB)
	diskInfo := p.collectDiskInfo()

	return map[string]any{
		"hostname": hostname,
		"os": map[string]any{
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
			"kernel":     kernel,
			"os_release": osVersion,
			"go_version": runtime.Version(),
			"build_info": readBuildInfo(),
		},
		"cpu":     cpuInfo,
		"memory":  memoryInfo,
		"disk":    diskInfo,
		"network": collectNetworkTraffic(),
		"host": map[string]any{
			"cpu": map[string]any{
				"load": map[string]any{
					"1m":  load1,
					"5m":  load5,
					"15m": load15,
				},
				"cores": runtime.NumCPU(),
			},
			"memory": map[string]any{
				"mem_total_kb":        hostMemTotalKB,
				"mem_available_kb":    hostMemAvailableKB,
				"mem_total_bytes":     hostMemTotalKB * 1024,
				"mem_available_bytes": hostMemAvailableKB * 1024,
			},
		},
	}
}

func detectOSVersion() string {
	if value := readTrimmed("/etc/os-release"); value != "" {
		scanner := bufio.NewScanner(strings.NewReader(value))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	return ""
}

func readHostMemInfo() (uint64, uint64) {
	raw := readTrimmed("/proc/meminfo")
	var total uint64
	var available uint64

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = value
		case "MemAvailable:":
			available = value
		}
	}
	return total, available
}

func collectCPUInfo(load1 float64, load5 float64, load15 float64) map[string]any {
	cores := runtime.NumCPU()
	source := "host"

	if limit, ok := readContainerCPULimit(); ok && limit > 0 {
		cores = limit
		source = "cgroup"
	}

	return map[string]any{
		"cores":       cores,
		"scope":       "container",
		"data_source": source,
		"load": map[string]any{
			"1m":  load1,
			"5m":  load5,
			"15m": load15,
		},
		"load_scope":       "host",
		"load_data_source": "/proc/loadavg",
	}
}

func collectMemoryInfo(hostTotalKB uint64, hostAvailableKB uint64) map[string]any {
	totalBytes, availableBytes, source := readContainerMemory()
	if totalBytes == 0 {
		totalBytes = hostTotalKB * 1024
		availableBytes = hostAvailableKB * 1024
		source = "host"
	}

	return map[string]any{
		"goroutines":          runtime.NumGoroutine(),
		"mem_total_kb":        totalBytes / 1024,
		"mem_available_kb":    availableBytes / 1024,
		"mem_total_bytes":     totalBytes,
		"mem_available_bytes": availableBytes,
		"scope":               "container",
		"data_source":         source,
	}
}

func (p *Profiler) collectDiskInfo() map[string]any {
	path := p.diskUsagePath
	if path == "" {
		path = "/config"
	}

	usedBytes, err := directorySize(path)
	if err != nil {
		return map[string]any{
			"path":        path,
			"scope":       "container_allocation",
			"data_source": "walk",
			"error":       err.Error(),
		}
	}

	freeBytes := uint64(0)
	if p.diskLimitBytes > usedBytes {
		freeBytes = p.diskLimitBytes - usedBytes
	}

	result := map[string]any{
		"path":            path,
		"used_bytes":      usedBytes,
		"root_used_bytes": usedBytes,
		"scope":           "container_allocation",
		"data_source":     "walk",
	}
	if p.diskLimitBytes > 0 {
		result["limit_bytes"] = p.diskLimitBytes
		result["free_bytes"] = freeBytes
		result["root_total_bytes"] = p.diskLimitBytes
		result["root_free_bytes"] = freeBytes
	}
	return result
}

func readLoadAvg() (float64, float64, float64) {
	fields := strings.Fields(readTrimmed("/proc/loadavg"))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	parse := func(value string) float64 {
		v, _ := strconv.ParseFloat(value, 64)
		return v
	}
	return parse(fields[0]), parse(fields[1]), parse(fields[2])
}

func collectNetworkTraffic() map[string]any {
	raw := readTrimmed("/proc/net/dev")
	if raw == "" {
		return map[string]any{
			"rx_bytes":    uint64(0),
			"tx_bytes":    uint64(0),
			"interfaces":  []map[string]any{},
			"scope":       "pod_network_namespace",
			"data_source": "/proc/net/dev",
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(raw))
	result := make([]map[string]any, 0)
	var totalRX uint64
	var totalTX uint64
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}

		rxBytes, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		txBytes, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			continue
		}

		totalRX += rxBytes
		totalTX += txBytes
		result = append(result, map[string]any{
			"name":     name,
			"rx_bytes": rxBytes,
			"tx_bytes": txBytes,
		})
	}

	return map[string]any{
		"rx_bytes":    totalRX,
		"tx_bytes":    totalTX,
		"interfaces":  result,
		"scope":       "pod_network_namespace",
		"data_source": "/proc/net/dev",
	}
}

func readContainerCPULimit() (int, bool) {
	if quota, period, ok := readCgroupV2CPUQuota(); ok {
		return quotaToCores(quota, period)
	}
	if quota, period, ok := readCgroupV1CPUQuota(); ok {
		return quotaToCores(quota, period)
	}
	if cpus, ok := readCpusetLimit(); ok && cpus > 0 {
		return cpus, true
	}
	return 0, false
}

func readContainerMemory() (uint64, uint64, string) {
	if current, max, ok := readCgroupV2Memory(); ok {
		available := uint64(0)
		if max > current {
			available = max - current
		}
		return max, available, "cgroup"
	}
	if current, max, ok := readCgroupV1Memory(); ok {
		available := uint64(0)
		if max > current {
			available = max - current
		}
		return max, available, "cgroup"
	}
	return 0, 0, ""
}

func readCgroupV2CPUQuota() (float64, float64, bool) {
	raw := readTrimmed("/sys/fs/cgroup/cpu.max")
	if raw == "" {
		return 0, 0, false
	}

	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[0] == "max" {
		return 0, 0, false
	}

	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, false
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || period <= 0 {
		return 0, 0, false
	}
	return quota, period, true
}

func readCgroupV1CPUQuota() (float64, float64, bool) {
	quota := readTrimmed("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period := readTrimmed("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if quota == "" || period == "" || quota == "-1" {
		return 0, 0, false
	}

	quotaValue, err := strconv.ParseFloat(quota, 64)
	if err != nil {
		return 0, 0, false
	}
	periodValue, err := strconv.ParseFloat(period, 64)
	if err != nil || periodValue <= 0 {
		return 0, 0, false
	}
	return quotaValue, periodValue, true
}

func quotaToCores(quota float64, period float64) (int, bool) {
	if quota <= 0 || period <= 0 {
		return 0, false
	}
	cores := int(math.Ceil(quota / period))
	if cores < 1 {
		cores = 1
	}
	return cores, true
}

func readCpusetLimit() (int, bool) {
	for _, path := range []string{
		"/sys/fs/cgroup/cpuset.cpus.effective",
		"/sys/fs/cgroup/cpuset/cpuset.cpus",
	} {
		raw := readTrimmed(path)
		if raw == "" {
			continue
		}
		if count := parseCPUSet(raw); count > 0 {
			return count, true
		}
	}
	return 0, false
}

func parseCPUSet(raw string) int {
	total := 0
	for _, part := range strings.Split(strings.TrimSpace(raw), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "-") {
			total++
			continue
		}
		bounds := strings.SplitN(part, "-", 2)
		if len(bounds) != 2 {
			continue
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil {
			continue
		}
		end, err := strconv.Atoi(bounds[1])
		if err != nil || end < start {
			continue
		}
		total += end - start + 1
	}
	return total
}

func readCgroupV2Memory() (uint64, uint64, bool) {
	current, ok := readUintFromFile("/sys/fs/cgroup/memory.current")
	if !ok {
		return 0, 0, false
	}
	max, ok := readCgroupMemoryMax("/sys/fs/cgroup/memory.max")
	if !ok {
		return 0, 0, false
	}
	return current, max, true
}

func readCgroupV1Memory() (uint64, uint64, bool) {
	current, ok := readUintFromFile("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	if !ok {
		return 0, 0, false
	}
	max, ok := readCgroupMemoryMax("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if !ok {
		return 0, 0, false
	}
	return current, max, true
}

func readCgroupMemoryMax(path string) (uint64, bool) {
	raw := readTrimmed(path)
	if raw == "" || raw == "max" {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 || value > (1<<62) {
		return 0, false
	}
	return value, true
}

func readUintFromFile(path string) (uint64, bool) {
	raw := readTrimmed(path)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func directorySize(root string) (uint64, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return uint64(info.Size()), nil
	}

	var total uint64
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total, err
}

func readBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return ""
	}
	var buf bytes.Buffer
	buf.WriteString(info.GoVersion)
	if info.Main.Path != "" {
		buf.WriteString(" ")
		buf.WriteString(info.Main.Path)
	}
	if info.Main.Version != "" {
		buf.WriteString("@")
		buf.WriteString(info.Main.Version)
	}
	return strings.TrimSpace(buf.String())
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
