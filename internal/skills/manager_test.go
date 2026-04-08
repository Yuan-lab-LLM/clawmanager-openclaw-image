package skills

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	appconfig "github.com/iamlovingit/clawmanager-openclaw-image/internal/config"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/protocol"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/store"
)

func TestHashEntryStableForDirectory(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(skillDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "nested", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	sum1, size1, count1, kind1, err := hashEntry(skillDir, entry[0])
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(filepath.Join(skillDir, "b.txt"), filepath.Join(skillDir, "c.txt")); err != nil {
		t.Fatal(err)
	}
	entry, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	sum2, _, _, _, err := hashEntry(skillDir, entry[0])
	if err != nil {
		t.Fatal(err)
	}

	if sum1 == sum2 {
		t.Fatalf("expected content hash to change when relative path changes")
	}
	if size1 != 2 || count1 != 2 || kind1 != "directory" {
		t.Fatalf("unexpected metadata size=%d count=%d kind=%s", size1, count1, kind1)
	}
}

func TestInventoryDigestChangesOnContent(t *testing.T) {
	now := time.Now().UTC()
	a := []protocol.SkillInventoryItem{{
		Identifier:  "demo",
		InstallPath: "/skills/demo",
		ContentMD5:  "aaa",
		Source:      protocol.SkillSourceInjected,
		CollectedAt: now,
	}}
	b := []protocol.SkillInventoryItem{{
		Identifier:  "demo",
		InstallPath: "/skills/demo",
		ContentMD5:  "bbb",
		Source:      protocol.SkillSourceInjected,
		CollectedAt: now,
	}}
	if inventoryDigest(a) == inventoryDigest(b) {
		t.Fatal("expected digest to change when content md5 changes")
	}
}

func TestDiscoverIncludesBuiltinAndWorkspaceSkills(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace-skills")
	builtin := filepath.Join(root, "builtin-skills")
	if err := os.MkdirAll(filepath.Join(workspace, "custom-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(builtin, "apple-notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "custom-skill", "SKILL.md"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtin, "apple-notes", "SKILL.md"), []byte("builtin"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}

	manager := New(appconfig.Config{
		OpenClawSkillsPath:        workspace,
		OpenClawBuiltinSkillsPath: builtin,
	}, noopClient{}, st)

	items, _, err := manager.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(items))
	}

	got := map[string]string{}
	for _, item := range items {
		got[item.Identifier] = item.Source
	}
	if got["custom-skill"] != protocol.SkillSourceDiscovered {
		t.Fatalf("unexpected custom-skill source: %s", got["custom-skill"])
	}
	if got["apple-notes"] != protocol.SkillSourceBuiltin {
		t.Fatalf("unexpected apple-notes source: %s", got["apple-notes"])
	}
}

func TestActivateSkillReplacesTarget(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := activateSkill(staging, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("unexpected target content: %s", string(data))
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected staging removed, got err=%v", err)
	}
}

func TestIsCrossDeviceError(t *testing.T) {
	if !isCrossDeviceError(syscall.EXDEV) {
		t.Fatal("expected EXDEV to be recognized")
	}
}

func TestPackageSkillArchiveUsesSingleTopLevelDirectory(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "writing-skills")
	if err := os.MkdirAll(filepath.Join(skillDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "nested", "data.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	md5sum, _, _, itemType, err := hashEntry(skillDir, entry[0])
	if err != nil {
		t.Fatal(err)
	}
	item := &protocol.SkillInventoryItem{
		Identifier:  "writing-skills",
		InstallPath: skillDir,
		ContentMD5:  md5sum,
		Type:        itemType,
	}
	archivePath := filepath.Join(root, "writing-skills.zip")
	if err := packageSkillArchive(item, archivePath); err != nil {
		t.Fatal(err)
	}

	extractDir := filepath.Join(root, "extract")
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := extractZIP(data, extractDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(extractDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected extracted content: %s", string(got))
	}
}

func TestFindSkillForUploadMatchesInventory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace-skills")
	if err := os.MkdirAll(filepath.Join(workspace, "demo-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "demo-skill", "SKILL.md"), []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(appconfig.Config{OpenClawSkillsPath: workspace}, noopClient{}, st)
	item, err := manager.findSkillForUpload(context.Background(), map[string]any{
		"identifier": "demo-skill",
		"source":     protocol.SkillSourceDiscovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Identifier != "demo-skill" || !strings.Contains(item.InstallPath, "demo-skill") {
		t.Fatalf("unexpected item: %+v", item)
	}
}

type noopClient struct{}

func (noopClient) ReportSkillInventory(context.Context, protocol.SkillInventoryReportRequest) error {
	return nil
}

func (noopClient) DownloadSkillArchive(context.Context, string) ([]byte, error) {
	return nil, nil
}

func (noopClient) UploadSkillArchive(context.Context, protocol.SkillUploadRequest, string, io.Reader) error {
	return nil
}
