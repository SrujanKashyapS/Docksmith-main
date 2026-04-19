package utils_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/SrujanKashyapS/Docksmith/utils"
)

func TestSplitImageRef(t *testing.T) {
	tests := []struct {
		input string
		name  string
		tag   string
	}{
		{"busybox:latest", "busybox", "latest"},
		{"busybox", "busybox", "latest"},
		{"my/image:v1.2.3", "my/image", "v1.2.3"},
		{"localhost:5000/myapp:dev", "localhost:5000/myapp", "dev"},
	}
	for _, tt := range tests {
		name, tag := utils.SplitImageRef(tt.input)
		if name != tt.name || tag != tt.tag {
			t.Errorf("SplitImageRef(%q) = (%q, %q), want (%q, %q)",
				tt.input, name, tag, tt.name, tt.tag)
		}
	}
}

func TestImageKey(t *testing.T) {
	tests := []struct {
		name, tag string
		want      string
	}{
		{"busybox", "latest", "busybox_latest"},
		{"my/image", "v1", "my_image_v1"},
	}
	for _, tt := range tests {
		got := utils.ImageKey(tt.name, tt.tag)
		if got != tt.want {
			t.Errorf("ImageKey(%q, %q) = %q, want %q", tt.name, tt.tag, got, tt.want)
		}
	}
}

func TestSHA256Bytes(t *testing.T) {
	got := utils.SHA256Bytes([]byte("hello"))
	// Known SHA256("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("SHA256Bytes(hello) = %q, want %q", got, want)
	}
}

func TestSHA256String(t *testing.T) {
	got := utils.SHA256String("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("SHA256String(hello) = %q, want %q", got, want)
	}
}

func TestSHA256File(t *testing.T) {
	tmp, err := os.CreateTemp("", "docksmith-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString("hello")
	tmp.Close()

	got, err := utils.SHA256File(tmp.Name())
	if err != nil {
		t.Fatalf("SHA256File: %v", err)
	}
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("SHA256File = %q, want %q", got, want)
	}
}

func TestScanDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "docksmith-scan-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("content a"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("content b"), 0o644)

	entries, err := utils.ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}

	if _, ok := entries["a.txt"]; !ok {
		t.Error("expected a.txt in scan results")
	}
	if _, ok := entries["sub"]; !ok {
		t.Error("expected sub/ in scan results")
	}
	subKey := filepath.Join("sub", "b.txt")
	if _, ok := entries[subKey]; !ok {
		t.Errorf("expected %q in scan results", subKey)
	}
}

func TestGlobFiles_Simple(t *testing.T) {
	dir, err := os.MkdirTemp("", "docksmith-glob-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	os.WriteFile(filepath.Join(dir, "hello.sh"), []byte("echo hi"), 0o644)
	os.WriteFile(filepath.Join(dir, "world.sh"), []byte("echo world"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme"), 0o644)

	matches, err := utils.GlobFiles(dir, "*.sh")
	if err != nil {
		t.Fatalf("GlobFiles: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matches), matches)
	}
}

func TestGlobFiles_DoubleGlob(t *testing.T) {
	dir, err := os.MkdirTemp("", "docksmith-glob2-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "b", "file.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "other.txt"), []byte("y"), 0o644)

	matches, err := utils.GlobFiles(dir, "**/*.txt")
	if err != nil {
		t.Fatalf("GlobFiles(**/*.txt): %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matches), matches)
	}
}

func TestCreateTarAndExtract(t *testing.T) {
	srcDir, err := os.MkdirTemp("", "docksmith-tar-src-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "subdir", "file2.txt"), []byte("world"), 0o644)

	tarBytes, err := utils.CreateTarFromDir(srcDir)
	if err != nil {
		t.Fatalf("CreateTarFromDir: %v", err)
	}
	if len(tarBytes) == 0 {
		t.Fatal("expected non-empty tar")
	}

	destDir, err := os.MkdirTemp("", "docksmith-tar-dest-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(destDir)

	if err := utils.ExtractTarReader(bytes.NewReader(tarBytes), destDir); err != nil {
		t.Fatalf("ExtractTarReader: %v", err)
	}

	content1, err := os.ReadFile(filepath.Join(destDir, "file1.txt"))
	if err != nil {
		t.Fatalf("reading file1.txt after extract: %v", err)
	}
	if string(content1) != "hello" {
		t.Errorf("file1.txt: expected 'hello', got %q", content1)
	}

	content2, err := os.ReadFile(filepath.Join(destDir, "subdir", "file2.txt"))
	if err != nil {
		t.Fatalf("reading subdir/file2.txt after extract: %v", err)
	}
	if string(content2) != "world" {
		t.Errorf("subdir/file2.txt: expected 'world', got %q", content2)
	}
}

func TestTarDeterminism(t *testing.T) {
	// Same files should produce identical tars.
	srcDir, err := os.MkdirTemp("", "docksmith-determ-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "b.txt"), []byte("bbb"), 0o644)

	tar1, err := utils.CreateTarFromDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	tar2, err := utils.CreateTarFromDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	h1 := utils.SHA256Bytes(tar1)
	h2 := utils.SHA256Bytes(tar2)
	if h1 != h2 {
		t.Errorf("tar not deterministic: %s != %s", h1, h2)
	}
}

func TestHashFiles(t *testing.T) {
	dir, err := os.MkdirTemp("", "docksmith-hash-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	f1 := filepath.Join(dir, "f1.txt")
	f2 := filepath.Join(dir, "f2.txt")
	os.WriteFile(f1, []byte("content1"), 0o644)
	os.WriteFile(f2, []byte("content2"), 0o644)

	// Hash should be deterministic.
	h1, err := utils.HashFiles([]string{f1, f2})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := utils.HashFiles([]string{f2, f1}) // different order
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("HashFiles not order-independent: %s != %s", h1, h2)
	}
}
