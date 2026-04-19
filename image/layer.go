package image

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SrujanKashyapS/Docksmith/utils"
)

// layerEntry represents a single file to be written into a layer tar.
type layerEntry struct {
	srcPath  string
	destPath string
}

// StoreLayer stores a tar byte slice as an immutable layer.
// Returns the layer's SHA256 digest.
func StoreLayer(tarBytes []byte) (string, error) {
	if err := utils.EnsureDirs(); err != nil {
		return "", err
	}
	digest := utils.SHA256Bytes(tarBytes)
	path, err := utils.LayerPath(digest)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		// Already exists (identical content).
		return digest, nil
	}
	if err := os.WriteFile(path, tarBytes, 0o644); err != nil {
		return "", fmt.Errorf("storing layer: %w", err)
	}
	return digest, nil
}

// LayerSize returns the size in bytes of a stored layer.
func LayerSize(digest string) (int64, error) {
	path, err := utils.LayerPath(digest)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ExtractLayers extracts a list of layer digests into destDir, in order.
func ExtractLayers(digests []string, destDir string) error {
	for _, digest := range digests {
		path, err := utils.LayerPath(digest)
		if err != nil {
			return err
		}
		if err := utils.ExtractTar(path, destDir); err != nil {
			return fmt.Errorf("extracting layer %s: %w", digest, err)
		}
	}
	return nil
}

// ExtractManifestLayers extracts all layers from a manifest into destDir.
func ExtractManifestLayers(m *Manifest, destDir string) error {
	var digests []string
	for _, l := range m.Layers {
		digests = append(digests, l.Digest)
	}
	return ExtractLayers(digests, destDir)
}

// CreateCopyLayer creates a layer tar that copies src files from contextDir
// to dest within the layer. src may be a file, directory, or glob pattern.
func CreateCopyLayer(contextDir, src, dest string) ([]byte, error) {
	matches, err := utils.GlobFiles(contextDir, src)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", src, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("COPY: no files match %q in context %s", src, contextDir)
	}

	var entries []layerEntry

	for _, match := range matches {
		info, err := os.Lstat(match)
		if err != nil {
			return nil, err
		}

		if info.IsDir() {
			// Walk the directory and add all files/dirs.
			err := filepath.Walk(match, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				rel, err := filepath.Rel(match, path)
				if err != nil {
					return err
				}
				var destRel string
				if rel == "." {
					destRel = dest
				} else {
					destRel = filepath.Join(dest, rel)
				}
				entries = append(entries, layerEntry{srcPath: path, destPath: destRel})
				return nil
			})
			if err != nil {
				return nil, err
			}
		} else {
			// Single file.
			var destPath string
			if strings.HasSuffix(dest, "/") || len(matches) > 1 {
				destPath = filepath.Join(dest, filepath.Base(match))
			} else {
				destPath = dest
			}
			entries = append(entries, layerEntry{srcPath: match, destPath: destPath})
		}
	}

	// Sort by destPath for determinism.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].destPath < entries[j].destPath
	})

	return writeTarEntries(entries)
}

// CreateDeltaLayer creates a tar of files in newRoot that differ from oldSnapshot.
// oldSnapshot is a map of relpath -> FileEntry (from utils.ScanDir).
func CreateDeltaLayer(newRoot string, oldSnapshot map[string]utils.FileEntry) ([]byte, error) {
	newSnapshot, err := utils.ScanDir(newRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning new root: %w", err)
	}

	// Find new or modified files.
	var changedPaths []string
	for rel, newEntry := range newSnapshot {
		oldEntry, exists := oldSnapshot[rel]
		if !exists || oldEntry.Hash != newEntry.Hash {
			changedPaths = append(changedPaths, rel)
		}
	}
	sort.Strings(changedPaths)

	if len(changedPaths) == 0 {
		// No changes: return minimal empty tar.
		pr, pw := io.Pipe()
		tw := tar.NewWriter(pw)
		go func() {
			tw.Close()
			pw.Close()
		}()
		return io.ReadAll(pr)
	}

	var entries []layerEntry
	for _, rel := range changedPaths {
		entries = append(entries, layerEntry{
			srcPath:  filepath.Join(newRoot, rel),
			destPath: rel,
		})
	}

	return writeTarEntries(entries)
}

// writeTarEntries writes a set of layerEntry items into a deterministic tar archive.
func writeTarEntries(entries []layerEntry) ([]byte, error) {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)
	errCh := make(chan error, 1)

	go func() {
		defer func() {
			tw.Close()
			pw.Close()
		}()
		for _, e := range entries {
			info, err := os.Lstat(e.srcPath)
			if err != nil {
				errCh <- err
				return
			}

			var hdr *tar.Header
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(e.srcPath)
				if err != nil {
					errCh <- err
					return
				}
				hdr = &tar.Header{
					Typeflag: tar.TypeSymlink,
					Linkname: link,
				}
			} else {
				hdr, err = tar.FileInfoHeader(info, "")
				if err != nil {
					errCh <- err
					return
				}
			}

			// Normalize dest path: no leading slash, forward slashes.
			destPath := filepath.ToSlash(strings.TrimPrefix(e.destPath, "/"))
			if info.IsDir() && !strings.HasSuffix(destPath, "/") {
				destPath += "/"
			}
			hdr.Name = destPath

			// Zero timestamps for reproducibility.
			hdr.ModTime = time.Time{}
			hdr.AccessTime = time.Time{}
			hdr.ChangeTime = time.Time{}
			hdr.Uid = 0
			hdr.Gid = 0
			hdr.Uname = ""
			hdr.Gname = ""

			if err := tw.WriteHeader(hdr); err != nil {
				errCh <- err
				return
			}
			if info.Mode().IsRegular() {
				f, err := os.Open(e.srcPath)
				if err != nil {
					errCh <- err
					return
				}
				_, copyErr := io.Copy(tw, f)
				f.Close()
				if copyErr != nil {
					errCh <- copyErr
					return
				}
			}
		}
		errCh <- nil
	}()

	data, readErr := io.ReadAll(pr)
	writeErr := <-errCh
	if writeErr != nil {
		return nil, writeErr
	}
	if readErr != nil {
		return nil, readErr
	}
	return data, nil
}
