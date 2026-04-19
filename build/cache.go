package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SrujanKashyapS/Docksmith/utils"
)

// CacheKey computes a deterministic cache key for a build step.
// The key includes:
//   - previous layer digest
//   - full instruction text
//   - current WORKDIR
//   - sorted ENV values
//   - for COPY: hash of source files
func CacheKey(prevDigest, rawInstruction, workdir string, envs []string, srcFileHash string) string {
	h := sha256.New()

	fmt.Fprintf(h, "prev:%s\n", prevDigest)
	fmt.Fprintf(h, "instruction:%s\n", rawInstruction)
	fmt.Fprintf(h, "workdir:%s\n", workdir)

	sorted := make([]string, len(envs))
	copy(sorted, envs)
	sort.Strings(sorted)
	for _, e := range sorted {
		fmt.Fprintf(h, "env:%s\n", e)
	}

	if srcFileHash != "" {
		fmt.Fprintf(h, "srchash:%s\n", srcFileHash)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// cacheEntryPath returns the path to the cache entry file for the given key.
func cacheEntryPath(key string) (string, error) {
	dir, err := utils.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, key), nil
}

// LookupCache checks if a cache entry exists for the given key.
// Returns the cached layer digest, or ("", false, nil) if not found.
func LookupCache(key string) (string, bool, error) {
	path, err := cacheEntryPath(key)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading cache entry: %w", err)
	}
	digest := strings.TrimSpace(string(data))
	if digest == "" {
		return "", false, nil
	}
	// Verify the layer file still exists.
	layerPath, err := utils.LayerPath(digest)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(layerPath); err != nil {
		// Layer file gone, cache invalid.
		_ = os.Remove(path)
		return "", false, nil
	}
	return digest, true, nil
}

// StoreCache stores a layer digest for the given cache key.
func StoreCache(key, layerDigest string) error {
	if err := utils.EnsureDirs(); err != nil {
		return err
	}
	path, err := cacheEntryPath(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(layerDigest), 0o644)
}

// HashSourceFiles computes a combined hash of the source files for a COPY instruction.
// src is relative to contextDir.
func HashSourceFiles(contextDir, src string) (string, error) {
	matches, err := utils.GlobFiles(contextDir, src)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", src, err)
	}
	if len(matches) == 0 {
		return utils.SHA256String(src), nil // no files → hash the pattern itself
	}

	// Collect all files (recursively for dirs).
	var files []string
	for _, match := range matches {
		info, err := os.Lstat(match)
		if err != nil {
			continue
		}
		if info.IsDir() {
			filepath.Walk(match, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if !fi.IsDir() {
					files = append(files, path)
				}
				return nil
			})
		} else {
			files = append(files, match)
		}
	}

	return utils.HashFiles(files)
}
