package image_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SrujanKashyapS/Docksmith/image"
	"github.com/SrujanKashyapS/Docksmith/utils"
)

// setupTestHome sets up a temporary home directory for Docksmith state.
func setupTestHome(t *testing.T) func() {
	t.Helper()
	tmp, err := os.MkdirTemp("", "docksmith-image-test-*")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	return func() {
		os.Setenv("HOME", original)
		os.RemoveAll(tmp)
	}
}

func TestManifestSaveAndLoad(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	m := &image.Manifest{
		Name:    "testimage",
		Tag:     "latest",
		Created: time.Now().UTC().Truncate(time.Second),
		Config: image.Config{
			Env:        []string{"PATH=/usr/bin", "HOME=/root"},
			Cmd:        []string{"/bin/sh"},
			WorkingDir: "/app",
		},
		Layers: []image.LayerInfo{
			{Digest: "abc123", Size: 1024, CreatedBy: "COPY . /app"},
		},
	}

	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Digest should be set.
	if m.Digest == "" {
		t.Error("expected Digest to be set after Save")
	}

	// Load it back.
	loaded, err := image.Load("testimage", "latest")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Name != m.Name {
		t.Errorf("Name: got %q, want %q", loaded.Name, m.Name)
	}
	if loaded.Tag != m.Tag {
		t.Errorf("Tag: got %q, want %q", loaded.Tag, m.Tag)
	}
	if loaded.Config.WorkingDir != "/app" {
		t.Errorf("WorkingDir: got %q, want /app", loaded.Config.WorkingDir)
	}
	if len(loaded.Layers) != 1 {
		t.Errorf("Layers: got %d, want 1", len(loaded.Layers))
	}
}

func TestManifestDigestDeterminism(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	m1 := &image.Manifest{
		Name:    "img",
		Tag:     "v1",
		Created: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Config: image.Config{
			Cmd:        []string{"/bin/sh"},
			WorkingDir: "/",
		},
	}
	if err := m1.Save(); err != nil {
		t.Fatal(err)
	}
	digest1 := m1.Digest

	// Save again with same data, digest should be the same.
	m2 := &image.Manifest{
		Name:    "img",
		Tag:     "v1",
		Created: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Config: image.Config{
			Cmd:        []string{"/bin/sh"},
			WorkingDir: "/",
		},
	}
	if err := m2.Save(); err != nil {
		t.Fatal(err)
	}
	if m2.Digest != digest1 {
		t.Errorf("digest not deterministic: %q != %q", m2.Digest, digest1)
	}
}

func TestManifestLoadNotFound(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	_, err := image.Load("nonexistent", "latest")
	if err == nil {
		t.Fatal("expected error loading nonexistent image, got nil")
	}
}

func TestManifestListAll(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// No images yet.
	manifests, err := image.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 0 {
		t.Errorf("expected 0 images, got %d", len(manifests))
	}

	// Add two images.
	for _, name := range []string{"alpha", "beta"} {
		m := &image.Manifest{
			Name:    name,
			Tag:     "latest",
			Created: time.Now().UTC(),
			Config:  image.Config{Cmd: []string{"/bin/sh"}, WorkingDir: "/"},
		}
		if err := m.Save(); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}

	manifests, err = image.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 2 {
		t.Errorf("expected 2 images, got %d", len(manifests))
	}
}

func TestManifestDelete(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	m := &image.Manifest{
		Name:    "todelete",
		Tag:     "latest",
		Created: time.Now().UTC(),
		Config:  image.Config{Cmd: []string{"/bin/sh"}, WorkingDir: "/"},
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	if err := image.Delete("todelete", "latest"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should no longer be loadable.
	_, err := image.Load("todelete", "latest")
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestStoreAndExtractLayer(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Create a source file to put in a layer.
	srcDir, err := os.MkdirTemp("", "docksmith-layer-src-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("layer content"), 0o644)

	tarBytes, err := utils.CreateTarFromDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	digest, err := image.StoreLayer(tarBytes)
	if err != nil {
		t.Fatalf("StoreLayer: %v", err)
	}
	if digest == "" {
		t.Fatal("expected non-empty digest")
	}

	// Extract the layer.
	destDir, err := os.MkdirTemp("", "docksmith-layer-dest-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(destDir)

	if err := image.ExtractLayers([]string{digest}, destDir); err != nil {
		t.Fatalf("ExtractLayers: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "test.txt"))
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(content) != "layer content" {
		t.Errorf("got %q, want %q", content, "layer content")
	}
}

func TestStoreLayerIdempotent(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	data := []byte("some tar data that isn't really a tar but tests idempotency")

	d1, err := image.StoreLayer(data)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := image.StoreLayer(data)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("storing same data twice gave different digests: %s != %s", d1, d2)
	}
}

func TestCreateCopyLayer(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	if err := utils.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Create a context directory with some files.
	contextDir, err := os.MkdirTemp("", "docksmith-copy-ctx-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(contextDir)

	os.WriteFile(filepath.Join(contextDir, "app.sh"), []byte("#!/bin/sh\necho hello"), 0o755)

	// Create the layer.
	tarBytes, err := image.CreateCopyLayer(contextDir, "app.sh", "/app/app.sh")
	if err != nil {
		t.Fatalf("CreateCopyLayer: %v", err)
	}
	if len(tarBytes) == 0 {
		t.Fatal("expected non-empty tar")
	}

	// Extract and verify.
	destDir, err := os.MkdirTemp("", "docksmith-copy-dest-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(destDir)

	if err := utils.ExtractTarReader(bytes.NewReader(tarBytes), destDir); err != nil {
		t.Fatalf("ExtractTarReader: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "app", "app.sh"))
	if err != nil {
		t.Fatalf("reading extracted app.sh: %v", err)
	}
	if string(content) != "#!/bin/sh\necho hello" {
		t.Errorf("got %q", content)
	}
}

func TestCreateCopyLayer_NoMatch(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	contextDir, err := os.MkdirTemp("", "docksmith-copy-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(contextDir)

	_, err = image.CreateCopyLayer(contextDir, "nonexistent.sh", "/app/")
	if err == nil {
		t.Fatal("expected error for no matching files, got nil")
	}
}
