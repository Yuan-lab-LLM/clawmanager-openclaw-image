package skills

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	appconfig "github.com/iamlovingit/clawmanager-openclaw-image/internal/config"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/protocol"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/store"
)

type inventoryClient interface {
	ReportSkillInventory(ctx context.Context, req protocol.SkillInventoryReportRequest) error
	DownloadSkillArchive(ctx context.Context, skillVersion string) ([]byte, error)
	UploadSkillArchive(ctx context.Context, req protocol.SkillUploadRequest, fileName string, file io.Reader) error
}

type Manager struct {
	cfg    appconfig.Config
	client inventoryClient
	store  *store.Store
}

func New(cfg appconfig.Config, client inventoryClient, st *store.Store) *Manager {
	return &Manager{cfg: cfg, client: client, store: st}
}

func (m *Manager) Discover(ctx context.Context) ([]protocol.SkillInventoryItem, string, error) {
	_ = ctx

	state := m.store.Snapshot()
	items := make([]protocol.SkillInventoryItem, 0, 64)
	now := time.Now().UTC()
	seen := map[string]struct{}{}

	sources := []skillSource{
		{root: m.cfg.OpenClawSkillsPath, source: protocol.SkillSourceDiscovered, scope: "workspace"},
		{root: m.cfg.OpenClawBuiltinSkillsPath, source: protocol.SkillSourceBuiltin, scope: "builtin"},
	}

	for _, source := range sources {
		discovered, err := collectFromRoot(source, now, state.ManagedSkills, seen)
		if err != nil {
			return nil, "", err
		}
		items = append(items, discovered...)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].InstallPath == items[j].InstallPath {
			return items[i].Identifier < items[j].Identifier
		}
		return items[i].InstallPath < items[j].InstallPath
	})
	return items, inventoryDigest(items), nil
}

func (m *Manager) Sync(ctx context.Context, mode string, trigger string, force bool) (map[string]any, error) {
	items, digest, err := m.Discover(ctx)
	if err != nil {
		return nil, err
	}

	state := m.store.Snapshot()
	if !force && mode == "incremental" && digest == state.LastSkillInventoryDigest {
		return map[string]any{
			"mode":             mode,
			"trigger":          trigger,
			"reported":         false,
			"skill_count":      len(items),
			"inventory_digest": digest,
		}, nil
	}

	err = m.client.ReportSkillInventory(ctx, protocol.SkillInventoryReportRequest{
		AgentID:    state.AgentID,
		ReportedAt: time.Now().UTC(),
		Mode:       mode,
		Trigger:    trigger,
		Skills:     items,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := m.store.Update(func(s *store.State) {
		s.LastSkillInventoryDigest = digest
		if mode == "full" {
			s.LastSkillFullSyncAt = now
		}
		s.LastSkillIncrementalSyncAt = now
	}); err != nil {
		return nil, err
	}

	return map[string]any{
		"mode":             mode,
		"trigger":          trigger,
		"reported":         true,
		"skill_count":      len(items),
		"inventory_digest": digest,
	}, nil
}

func (m *Manager) Install(ctx context.Context, payload map[string]any) (map[string]any, error) {
	skillVersion := stringFromPayload(payload, "skill_version", "skill_version_id")
	if skillVersion == "" {
		return nil, errors.New("skill_version is required")
	}

	targetName := stringFromPayload(payload, "target_name", "identifier", "skill_name")
	if targetName == "" {
		targetName = "skill-" + skillVersion
	}
	targetPath := filepath.Join(m.cfg.OpenClawSkillsPath, filepath.Base(targetName))
	if raw := stringFromPayload(payload, "target_path"); raw != "" {
		targetPath = filepath.Clean(raw)
	}
	if err := ensureWithinRoot(m.cfg.OpenClawSkillsPath, targetPath); err != nil {
		return nil, err
	}

	expectedMD5 := strings.TrimSpace(stringFromPayload(payload, "content_md5", "content_hash", "md5"))
	existing, err := currentItem(targetPath)
	if err != nil {
		return nil, err
	}
	if existing != nil && expectedMD5 != "" && strings.EqualFold(existing.ContentMD5, expectedMD5) {
		if err := m.persistManagedSkill(targetPath, existing.ContentMD5, skillVersion, payload, "installed"); err != nil {
			return nil, err
		}
		return map[string]any{
			"status":       "noop_existing_content",
			"install_path": targetPath,
			"content_md5":  existing.ContentMD5,
		}, nil
	}

	blob, err := m.client.DownloadSkillArchive(ctx, skillVersion)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir skill parent: %w", err)
	}

	tempRoot := filepath.Join(filepath.Dir(targetPath), ".skill-staging")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir skill staging: %w", err)
	}
	stagingPath := filepath.Join(tempRoot, filepath.Base(targetPath)+"-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	defer func() {
		_ = os.RemoveAll(stagingPath)
	}()
	if err := extractArchive(blob, stagingPath); err != nil {
		return nil, err
	}

	staged, err := currentItem(stagingPath)
	if err != nil {
		_ = os.RemoveAll(stagingPath)
		return nil, err
	}
	if staged == nil {
		_ = os.RemoveAll(stagingPath)
		return nil, errors.New("skill archive produced empty content")
	}
	if expectedMD5 != "" && !strings.EqualFold(staged.ContentMD5, expectedMD5) {
		_ = os.RemoveAll(stagingPath)
		return nil, fmt.Errorf("skill md5 mismatch: expected %s got %s", expectedMD5, staged.ContentMD5)
	}

	if err := activateSkill(stagingPath, targetPath); err != nil {
		return nil, fmt.Errorf("activate skill: %w", err)
	}
	if err := m.persistManagedSkill(targetPath, staged.ContentMD5, skillVersion, payload, "installed"); err != nil {
		return nil, err
	}

	return map[string]any{
		"status":        "installed",
		"install_path":  targetPath,
		"content_md5":   staged.ContentMD5,
		"skill_version": skillVersion,
	}, nil
}

func (m *Manager) Uninstall(payload map[string]any) (map[string]any, error) {
	targetPath := m.resolveTargetPath(payload)
	if targetPath == "" {
		return nil, errors.New("target_path or identifier is required")
	}
	if err := ensureWithinRoot(m.cfg.OpenClawSkillsPath, targetPath); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove skill: %w", err)
	}
	if err := m.store.Update(func(s *store.State) {
		delete(s.ManagedSkills, targetPath)
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":       "removed",
		"install_path": targetPath,
	}, nil
}

func (m *Manager) Disable(payload map[string]any) (map[string]any, error) {
	targetPath := m.resolveTargetPath(payload)
	if targetPath == "" {
		return nil, errors.New("target_path or identifier is required")
	}
	if err := ensureWithinRoot(m.cfg.OpenClawSkillsPath, targetPath); err != nil {
		return nil, err
	}

	quarantineRoot := filepath.Join(m.cfg.AgentDataDir, "disabled-skills")
	if err := os.MkdirAll(quarantineRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir disabled skills: %w", err)
	}
	targetName := filepath.Base(targetPath)
	disabledPath := filepath.Join(quarantineRoot, targetName+"-"+fmt.Sprintf("%d", time.Now().Unix()))
	if err := os.Rename(targetPath, disabledPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{
				"status":       "noop_missing",
				"install_path": targetPath,
			}, nil
		}
		return nil, fmt.Errorf("disable skill: %w", err)
	}

	if err := m.store.Update(func(s *store.State) {
		record := s.ManagedSkills[targetPath]
		record.InstallPath = disabledPath
		record.Status = "disabled"
		record.UpdatedAt = time.Now().UTC()
		delete(s.ManagedSkills, targetPath)
		s.ManagedSkills[disabledPath] = record
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":        "disabled",
		"previous_path": targetPath,
		"disabled_path": disabledPath,
	}, nil
}

func (m *Manager) CollectPackage(ctx context.Context, payload map[string]any) (map[string]any, error) {
	item, err := m.findSkillForUpload(ctx, payload)
	if err != nil {
		return nil, err
	}

	stageDir := filepath.Join(m.cfg.AgentDataDir, "skill-package-staging")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir skill package staging: %w", err)
	}
	archivePath := filepath.Join(stageDir, item.Identifier+"-"+fmt.Sprintf("%d", time.Now().UnixNano())+".zip")
	defer func() {
		_ = os.Remove(archivePath)
	}()

	if err := packageSkillArchive(item, archivePath); err != nil {
		return nil, err
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open packaged skill archive: %w", err)
	}
	defer file.Close()

	uploadReq := protocol.SkillUploadRequest{
		AgentID:      m.store.Snapshot().AgentID,
		SkillID:      stringFromPayload(payload, "skill_id"),
		SkillVersion: stringFromPayload(payload, "skill_version", "skill_version_id"),
		Identifier:   item.Identifier,
		ContentMD5:   item.ContentMD5,
		Source:       item.Source,
	}
	if err := m.client.UploadSkillArchive(ctx, uploadReq, filepath.Base(archivePath), file); err != nil {
		return nil, err
	}

	info, err := os.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("stat packaged skill archive: %w", err)
	}
	return map[string]any{
		"status":         "uploaded",
		"identifier":     item.Identifier,
		"install_path":   item.InstallPath,
		"content_md5":    item.ContentMD5,
		"archive_format": "zip",
		"archive_name":   filepath.Base(archivePath),
		"archive_bytes":  info.Size(),
		"source":         item.Source,
		"skill_id":       uploadReq.SkillID,
		"skill_version":  uploadReq.SkillVersion,
	}, nil
}

func (m *Manager) resolveTargetPath(payload map[string]any) string {
	if raw := stringFromPayload(payload, "target_path", "install_path"); raw != "" {
		return filepath.Clean(raw)
	}
	if raw := stringFromPayload(payload, "target_name", "identifier", "skill_name"); raw != "" {
		return filepath.Join(m.cfg.OpenClawSkillsPath, filepath.Base(raw))
	}
	return ""
}

func (m *Manager) persistManagedSkill(targetPath, contentMD5, skillVersion string, payload map[string]any, status string) error {
	return m.store.Update(func(s *store.State) {
		record := s.ManagedSkills[targetPath]
		record.SkillID = stringFromPayload(payload, "skill_id")
		record.SkillVersion = skillVersion
		record.InstallPath = targetPath
		record.ContentMD5 = contentMD5
		record.Source = protocol.SkillSourceInjected
		record.Status = status
		if record.InstalledAt.IsZero() {
			record.InstalledAt = time.Now().UTC()
		}
		record.UpdatedAt = time.Now().UTC()
		s.ManagedSkills[targetPath] = record
	})
}

func collectInventoryItem(path string, entry fs.DirEntry, now time.Time, managed map[string]store.ManagedSkillRecord) (protocol.SkillInventoryItem, error) {
	return collectInventoryItemWithSource(path, entry, now, managed, protocol.SkillSourceDiscovered, "")
}

func collectInventoryItemWithSource(path string, entry fs.DirEntry, now time.Time, managed map[string]store.ManagedSkillRecord, defaultSource string, scope string) (protocol.SkillInventoryItem, error) {
	md5sum, sizeBytes, fileCount, itemType, err := hashEntry(path, entry)
	if err != nil {
		return protocol.SkillInventoryItem{}, err
	}
	source := defaultSource
	skillID := ""
	skillVersion := ""
	if record, ok := managed[path]; ok {
		source = firstNonEmpty(record.Source, defaultSource)
		skillID = record.SkillID
		skillVersion = record.SkillVersion
	}
	metadata := map[string]any{
		"entry_name": entry.Name(),
	}
	if scope != "" {
		metadata["scope"] = scope
	}
	return protocol.SkillInventoryItem{
		SkillID:      skillID,
		SkillVersion: skillVersion,
		Identifier:   strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
		InstallPath:  path,
		ContentMD5:   md5sum,
		Source:       source,
		Type:         itemType,
		SizeBytes:    sizeBytes,
		FileCount:    fileCount,
		CollectedAt:  now,
		Metadata:     metadata,
	}, nil
}

func currentItem(path string) (*protocol.SkillInventoryItem, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat skill path: %w", err)
	}
	item, err := collectInventoryItem(path, fileInfoDirEntry{FileInfo: info}, time.Now().UTC(), nil)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (m *Manager) findSkillForUpload(ctx context.Context, payload map[string]any) (*protocol.SkillInventoryItem, error) {
	items, _, err := m.Discover(ctx)
	if err != nil {
		return nil, err
	}
	identifier := stringFromPayload(payload, "identifier", "target_name", "skill_name")
	contentMD5 := strings.TrimSpace(stringFromPayload(payload, "content_md5", "content_hash", "md5"))
	source := strings.TrimSpace(stringFromPayload(payload, "source"))

	var matches []protocol.SkillInventoryItem
	for _, item := range items {
		if identifier != "" && item.Identifier != identifier {
			continue
		}
		if contentMD5 != "" && !strings.EqualFold(item.ContentMD5, contentMD5) {
			continue
		}
		if source != "" && item.Source != source {
			continue
		}
		matches = append(matches, item)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("skill not found for identifier=%q content_md5=%q", identifier, contentMD5)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple skills matched identifier=%q content_md5=%q", identifier, contentMD5)
	}
	return &matches[0], nil
}

func inventoryDigest(items []protocol.SkillInventoryItem) string {
	hash := md5.New()
	for _, item := range items {
		_, _ = io.WriteString(hash, item.InstallPath)
		_, _ = io.WriteString(hash, "\n")
		_, _ = io.WriteString(hash, item.ContentMD5)
		_, _ = io.WriteString(hash, "\n")
		_, _ = io.WriteString(hash, item.Source)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func hashEntry(path string, entry fs.DirEntry) (string, int64, int, string, error) {
	hash := md5.New()
	var totalSize int64
	var fileCount int

	if !entry.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", 0, 0, "", fmt.Errorf("read skill file %s: %w", path, err)
		}
		_, _ = io.WriteString(hash, "file\n")
		_, _ = io.WriteString(hash, entry.Name())
		_, _ = io.WriteString(hash, "\n")
		_, _ = hash.Write(data)
		return hex.EncodeToString(hash.Sum(nil)), int64(len(data)), 1, "file", nil
	}

	err := filepath.WalkDir(path, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		_, _ = io.WriteString(hash, rel)
		_, _ = io.WriteString(hash, "\n")
		if d.IsDir() {
			_, _ = io.WriteString(hash, "dir\n")
			return nil
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		totalSize += int64(len(data))
		fileCount++
		_, _ = io.WriteString(hash, "file\n")
		_, _ = hash.Write(data)
		_, _ = io.WriteString(hash, "\n")
		return nil
	})
	if err != nil {
		return "", 0, 0, "", fmt.Errorf("walk skill dir %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), totalSize, fileCount, "directory", nil
}

func extractArchive(blob []byte, targetPath string) error {
	if len(blob) >= 4 && bytes.Equal(blob[:4], []byte("PK\x03\x04")) {
		return extractZIP(blob, targetPath)
	}
	return extractTarGz(blob, targetPath)
}

func packageSkillArchive(item *protocol.SkillInventoryItem, archivePath string) error {
	if item == nil {
		return errors.New("skill item is required")
	}
	if item.Identifier == "" {
		return errors.New("skill identifier is required")
	}
	if item.InstallPath == "" {
		return errors.New("skill install path is required")
	}

	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return fmt.Errorf("mkdir archive dir: %w", err)
	}
	out, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	topLevel := filepath.Base(item.Identifier)
	info, err := os.Stat(item.InstallPath)
	if err != nil {
		return fmt.Errorf("stat skill install path: %w", err)
	}

	if !info.IsDir() {
		return writeZipFile(zw, item.InstallPath, filepath.ToSlash(filepath.Join(topLevel, filepath.Base(item.InstallPath))), info)
	}

	if _, err := zw.Create(topLevel + "/"); err != nil {
		return fmt.Errorf("write zip root dir: %w", err)
	}

	return filepath.Walk(item.InstallPath, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == item.InstallPath {
			return nil
		}
		rel, err := filepath.Rel(item.InstallPath, current)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(topLevel, rel))
		if info.IsDir() {
			_, err := zw.Create(name + "/")
			if err != nil {
				return fmt.Errorf("write zip dir: %w", err)
			}
			return nil
		}
		return writeZipFile(zw, current, name, info)
	})
}

func writeZipFile(zw *zip.Writer, srcPath string, archiveName string, info os.FileInfo) error {
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("build zip header: %w", err)
	}
	header.Name = archiveName
	header.Method = zip.Deflate
	header.Modified = time.Unix(0, 0)
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("write zip header: %w", err)
	}
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("write zip file body: %w", err)
	}
	return nil
}

func extractZIP(blob []byte, targetPath string) error {
	reader, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	for _, file := range reader.File {
		if err := writeArchiveEntry(file.Name, file.Mode(), file.FileInfo().IsDir(), func() (io.ReadCloser, error) {
			return file.Open()
		}, targetPath); err != nil {
			return err
		}
	}
	return flattenSingleTopLevelDir(targetPath)
}

func extractTarGz(blob []byte, targetPath string) error {
	gzr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		mode := fs.FileMode(header.Mode)
		isDir := header.FileInfo().IsDir()
		if err := writeArchiveEntry(header.Name, mode, isDir, func() (io.ReadCloser, error) {
			return io.NopCloser(tr), nil
		}, targetPath); err != nil {
			return err
		}
	}
	return flattenSingleTopLevelDir(targetPath)
}

func writeArchiveEntry(name string, mode fs.FileMode, isDir bool, open func() (io.ReadCloser, error), targetPath string) error {
	cleanName := filepath.Clean(name)
	if cleanName == "." || cleanName == "/" {
		return nil
	}
	dest := filepath.Join(targetPath, cleanName)
	if !strings.HasPrefix(dest, filepath.Clean(targetPath)+string(os.PathSeparator)) && filepath.Clean(dest) != filepath.Clean(targetPath) {
		return fmt.Errorf("invalid archive entry path %s", name)
	}
	if isDir {
		return os.MkdirAll(dest, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	rc, err := open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm(mode))
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func flattenSingleTopLevelDir(targetPath string) error {
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	root := filepath.Join(targetPath, entries[0].Name())
	children, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := os.Rename(filepath.Join(root, child.Name()), filepath.Join(targetPath, child.Name())); err != nil {
			return err
		}
	}
	return os.Remove(root)
}

func activateSkill(stagingPath string, targetPath string) error {
	if err := os.RemoveAll(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stagingPath, targetPath); err == nil {
		return nil
	} else if !isCrossDeviceError(err) {
		return err
	}

	if err := copyPath(stagingPath, targetPath); err != nil {
		return err
	}
	return os.RemoveAll(stagingPath)
}

func isCrossDeviceError(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}

func copyPath(src string, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	if err := os.MkdirAll(dst, dirPerm(info.Mode())); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcChild := filepath.Join(src, entry.Name())
		dstChild := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyPath(srcChild, dstChild); err != nil {
				return err
			}
			continue
		}
		childInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if err := copyFile(srcChild, dstChild, childInfo.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src string, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm(mode))
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func dirPerm(mode fs.FileMode) fs.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o755
	}
	return perm
}

func stringFromPayload(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64:
			return fmt.Sprintf("%.0f", v)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type fileInfoDirEntry struct {
	fs.FileInfo
}

type skillSource struct {
	root   string
	source string
	scope  string
}

func (f fileInfoDirEntry) Type() fs.FileMode          { return f.Mode().Type() }
func (f fileInfoDirEntry) Info() (fs.FileInfo, error) { return f.FileInfo, nil }

func filePerm(mode fs.FileMode) fs.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}

func ensureWithinRoot(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve skill path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("skill target path %s escapes root %s", target, root)
	}
	return nil
}

func collectFromRoot(source skillSource, now time.Time, managed map[string]store.ManagedSkillRecord, seen map[string]struct{}) ([]protocol.SkillInventoryItem, error) {
	if strings.TrimSpace(source.root) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(source.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills dir %s: %w", source.root, err)
	}

	items := make([]protocol.SkillInventoryItem, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".disabled") {
			continue
		}
		fullPath := filepath.Join(source.root, name)
		if _, ok := seen[fullPath]; ok {
			continue
		}
		item, err := collectInventoryItemWithSource(fullPath, entry, now, managed, source.source, source.scope)
		if err != nil {
			return nil, err
		}
		if item.Identifier == "" {
			continue
		}
		seen[fullPath] = struct{}{}
		items = append(items, item)
	}
	return items, nil
}
