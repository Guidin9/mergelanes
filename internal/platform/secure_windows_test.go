package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These need an elevated test process (SecureDir sets owners and access lists); they skip
// otherwise. Run them with: go test -c ./internal/platform && start the .exe as admin.

func needAdmin(t *testing.T) {
	t.Helper()
	if !IsAdmin() {
		t.Skip("needs an elevated test process")
	}
}

func trustedDir(t *testing.T, path string) bool {
	t.Helper()
	h, err := openNoFollow(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer closeHandle(h)
	plain, trusted := inspect(h)
	return plain && trusted
}

func TestSecureDirCreatesAndKeepsTrustedFolder(t *testing.T) {
	needAdmin(t)
	p := filepath.Join(t.TempDir(), "state")
	if err := SecureDir(p, false, true); err != nil {
		t.Fatal(err)
	}
	if !trustedDir(t, p) {
		t.Fatal("a folder SecureDir created is not trusted")
	}
	os.WriteFile(filepath.Join(p, "keep.json"), []byte("{}"), 0o644)
	if err := SecureDir(p, false, true); err != nil { // second start: same folder, contents kept
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p, "keep.json")); err != nil {
		t.Error("a trusted folder lost its contents")
	}
	if err := SecureDir(filepath.Join(p, "ctl"), true, true); err != nil {
		t.Fatal(err)
	}
	if !trustedDir(t, filepath.Join(p, "ctl")) {
		t.Error("the drop box is not trusted")
	}
}

func TestSecureDirMovesAsideAFolderItDidNotMake(t *testing.T) {
	needAdmin(t)
	base := t.TempDir()
	p := filepath.Join(base, "state")
	os.Mkdir(p, 0o755) // inherited access list, not protected: not one of ours
	os.WriteFile(filepath.Join(p, "task.xml"), []byte("planted"), 0o644)
	if err := SecureDir(p, false, true); err != nil {
		t.Fatal(err)
	}
	if !trustedDir(t, p) {
		t.Fatal("not trusted after replacing")
	}
	if _, err := os.Stat(filepath.Join(p, "task.xml")); err == nil {
		t.Error("a planted file is still in the new folder")
	}
	aside, _ := filepath.Glob(p + ".untrusted-*")
	if len(aside) != 1 {
		t.Errorf("want the old folder moved aside, found %v", aside)
	}
}

func TestSecureDirRemovesAJunctionNotItsTarget(t *testing.T) {
	needAdmin(t)
	base := t.TempDir()
	target := filepath.Join(base, "target")
	os.Mkdir(target, 0o755)
	os.WriteFile(filepath.Join(target, "victim.txt"), []byte("x"), 0o644)
	p := filepath.Join(base, "state")
	if out, err := exec.Command(SystemTool("cmd.exe"), "/c", "mklink", "/J", p, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}
	if err := SecureDir(p, false, true); err != nil {
		t.Fatal(err)
	}
	if !trustedDir(t, p) {
		t.Fatal("the junction was not replaced by a trusted folder")
	}
	if _, err := os.Stat(filepath.Join(target, "victim.txt")); err != nil {
		t.Error("the junction's target lost a file")
	}
	h, err := openNoFollow(target)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHandle(h)
	if _, trusted := inspect(h); trusted {
		t.Error("the junction's target got our access list")
	}
}

func TestSecureDirReplacesAFile(t *testing.T) {
	needAdmin(t)
	p := filepath.Join(t.TempDir(), "state")
	os.WriteFile(p, []byte("not a folder"), 0o644)
	if err := SecureDir(p, false, true); err != nil {
		t.Fatal(err)
	}
	if !trustedDir(t, p) {
		t.Error("a file in the way was not replaced by a trusted folder")
	}
}

func TestSecureDirLeavesAChosenFolderAlone(t *testing.T) {
	needAdmin(t)
	p := filepath.Join(t.TempDir(), "mine")
	os.Mkdir(p, 0o755)
	os.WriteFile(filepath.Join(p, "limits.json"), []byte("{}"), 0o644)
	err := SecureDir(p, false, false)
	if err == nil || !strings.Contains(err.Error(), "only administrators") {
		t.Fatalf("want an error for a folder the user chose, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(p, "limits.json")); err != nil {
		t.Error("the user's folder was changed")
	}
}
