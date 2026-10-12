package sysmac

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveStudioInstallPrefersExplicitThenEnv(t *testing.T) {
	t.Setenv(StudioInstallEnv, `X:\FromEnv`)
	if got := ResolveStudioInstall(`Y:\Explicit`); got != filepath.Clean(`Y:\Explicit`) {
		t.Fatalf("explicit path must win, got %q", got)
	}
	if got := ResolveStudioInstall(""); got != filepath.Clean(`X:\FromEnv`) {
		t.Fatalf("env path must be used, got %q", got)
	}
}

func TestIsStudioInstallRequiresBuilderFiles(t *testing.T) {
	dir := t.TempDir()
	if isStudioInstall(dir) {
		t.Fatal("empty dir is not an install")
	}
	for _, rel := range []string{filepath.Join("Modules", "Nex", "INexBuilder2.dll"), filepath.Join("builder2", "NexBuilder2.dll")} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755)
		os.WriteFile(filepath.Join(dir, rel), []byte("x"), 0o600)
	}
	if !isStudioInstall(dir) {
		t.Fatal("dir with builder files must be detected")
	}
}

func TestCheckInputsStale(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	os.MkdirAll(cache, 0o755)
	old := time.Now().Add(-time.Hour)
	input := filepath.Join(cache, "LV_x.cxil2")
	os.WriteFile(input, []byte("x"), 0o600)
	os.Chtimes(input, old, old)
	source := filepath.Join(root, "p.oem")
	os.WriteFile(source, []byte("x"), 0o600)
	if stale, msg := checkInputsStale(root, cache); !stale || msg == "" {
		t.Fatal("source newer than inputs must be reported stale")
	}
	os.Chtimes(source, old.Add(-time.Hour), old.Add(-time.Hour))
	if stale, _ := checkInputsStale(root, cache); stale {
		t.Fatal("inputs newer than source must not be stale")
	}
}
