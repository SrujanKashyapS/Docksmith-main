package image

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SrujanKashyapS/Docksmith/utils"
)

// Config holds the image configuration (env, cmd, workingDir).
type Config struct {
	Env        []string `json:"env"`
	Cmd        []string `json:"cmd"`
	WorkingDir string   `json:"workingDir"`
}

// LayerInfo describes a single layer in an image.
type LayerInfo struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"createdBy"`
}

// Manifest is the image manifest stored in ~/.docksmith/images/.
type Manifest struct {
	Name    string      `json:"name"`
	Tag     string      `json:"tag"`
	Digest  string      `json:"digest"`
	Created time.Time   `json:"created"`
	Config  Config      `json:"config"`
	Layers  []LayerInfo `json:"layers"`
}

// manifestPath returns the path to the manifest file for name:tag.
func manifestPath(name, tag string) (string, error) {
	dir, err := utils.ImagesDir()
	if err != nil {
		return "", err
	}
	key := utils.ImageKey(name, tag)
	return filepath.Join(dir, key+".json"), nil
}

// Save writes the manifest to disk, computing its digest.
func (m *Manifest) Save() error {
	if err := utils.EnsureDirs(); err != nil {
		return err
	}

	// Compute digest: SHA256 of JSON with empty digest field.
	tmp := *m
	tmp.Digest = ""
	data, err := json.Marshal(tmp)
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}
	h := sha256.New()
	h.Write(data)
	m.Digest = "sha256:" + hex.EncodeToString(h.Sum(nil))

	// Marshal with digest filled in.
	final, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling manifest with digest: %w", err)
	}

	path, err := manifestPath(m.Name, m.Tag)
	if err != nil {
		return err
	}
	return os.WriteFile(path, final, 0o644)
}

// Load reads the manifest for name:tag from disk.
func Load(name, tag string) (*Manifest, error) {
	path, err := manifestPath(name, tag)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("image %s:%s not found in local store", name, tag)
		}
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return &m, nil
}

// Delete removes the manifest and all layers belonging to this image.
func Delete(name, tag string) error {
	m, err := Load(name, tag)
	if err != nil {
		return err // Manifest must exist to be deleted
	}

	path, err := manifestPath(name, tag)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("image %s:%s not found", name, tag)
		}
		return err
	}

	// Constraint: immutable layers, rmi deletes the layer files belonging to the removed image
	for _, l := range m.Layers {
		layerPath, err := utils.LayerPath(l.Digest)
		if err == nil {
			_ = os.Remove(layerPath)
		}
	}

	return nil
}

// ListAll returns all manifests stored locally.
func ListAll() ([]*Manifest, error) {
	dir, err := utils.ImagesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var result []*Manifest
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		result = append(result, &m)
	}
	return result, nil
}
