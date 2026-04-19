package utils

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DocksmithRoot returns the root directory for all Docksmith state.
func DocksmithRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".docksmith"), nil
}

// EnsureDirs creates all required Docksmith directories.
func EnsureDirs() error {
	root, err := DocksmithRoot()
	if err != nil {
		return err
	}
	for _, sub := range []string{"images", "layers", "cache"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return fmt.Errorf("creating %s dir: %w", sub, err)
		}
	}
	return nil
}

// ImagesDir returns the path to the images directory.
func ImagesDir() (string, error) {
	root, err := DocksmithRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "images"), nil
}

// LayersDir returns the path to the layers directory.
func LayersDir() (string, error) {
	root, err := DocksmithRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "layers"), nil
}

// CacheDir returns the path to the cache directory.
func CacheDir() (string, error) {
	root, err := DocksmithRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "cache"), nil
}

// SHA256File computes the SHA256 digest of a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256Bytes computes the SHA256 digest of a byte slice.
func SHA256Bytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256String computes the SHA256 digest of a string.
func SHA256String(s string) string {
	return SHA256Bytes([]byte(s))
}

// LayerPath returns the filesystem path for a layer given its digest.
func LayerPath(digest string) (string, error) {
	dir, err := LayersDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, digest+".tar"), nil
}

// FileEntry represents a file's state for change detection.
type FileEntry struct {
	RelPath string
	Hash    string
	Mode    os.FileMode
	IsDir   bool
	Symlink string
}

// ScanDir scans a directory and returns a map of relpath -> FileEntry.
func ScanDir(root string) (map[string]FileEntry, error) {
	result := make(map[string]FileEntry)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // continue walking despite errors on individual entries
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entry := FileEntry{
			RelPath: rel,
			Mode:    info.Mode(),
			IsDir:   info.IsDir(),
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ := os.Readlink(path)
			entry.Symlink = link
			entry.Hash = SHA256String(link)
		} else if !info.IsDir() {
			h, err := SHA256File(path)
			if err != nil {
				return nil
			}
			entry.Hash = h
		}
		result[rel] = entry
		return nil
	})
	return result, err
}

// CreateTarFromPaths creates a deterministic tar archive from a list of paths
// rooted at srcRoot and destined for destBase in the archive.
// Entries are sorted, timestamps zeroed.
func CreateTarFromPaths(srcRoot string, paths []string, destBase string) ([]byte, error) {
	// Sort for determinism.
	sort.Strings(paths)

	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)

	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		for _, relPath := range paths {
			srcPath := filepath.Join(srcRoot, relPath)
			info, err := os.Lstat(srcPath)
			if err != nil {
				continue
			}

			var destPath string
			if destBase != "" {
				destPath = filepath.Join(destBase, relPath)
			} else {
				destPath = relPath
			}
			// Normalize separators and trim leading slash.
			destPath = filepath.ToSlash(strings.TrimPrefix(destPath, "/"))

			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				errCh <- err
				return
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

			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(srcPath)
				if err != nil {
					errCh <- err
					return
				}
				hdr.Linkname = link
			}

			if err := tw.WriteHeader(hdr); err != nil {
				errCh <- err
				return
			}
			if info.Mode().IsRegular() {
				f, err := os.Open(srcPath)
				if err != nil {
					errCh <- err
					return
				}
				if _, err := io.Copy(tw, f); err != nil {
					f.Close()
					errCh <- err
					return
				}
				f.Close()
			}
		}
		if err := tw.Close(); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	out, readErr := io.ReadAll(pr)
	writeErr := <-errCh
	if writeErr != nil {
		return nil, writeErr
	}
	if readErr != nil {
		return nil, readErr
	}
	return out, nil
}

// CreateTarFromDir creates a deterministic tar of an entire directory.
func CreateTarFromDir(srcDir string) ([]byte, error) {
	var paths []string
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return CreateTarFromPaths(srcDir, paths, "")
}

// ExtractTar extracts a tar archive into destDir.
func ExtractTar(tarPath, destDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("opening tar %s: %w", tarPath, err)
	}
	defer f.Close()
	return ExtractTarReader(f, destDir)
}

// ExtractTarReader extracts a tar archive from a reader into destDir.
func ExtractTarReader(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		target := filepath.Join(destDir, filepath.FromSlash(hdr.Name))

		// Security: prevent path traversal.
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, hdr.FileInfo().Mode()); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target) // ignore error if not exists
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget := filepath.Join(destDir, filepath.FromSlash(hdr.Linkname))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// HashFiles computes a combined hash of multiple files (sorted by path for determinism).
func HashFiles(paths []string) (string, error) {
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fh, err := SHA256File(p)
		if err != nil {
			return "", fmt.Errorf("hashing %s: %w", p, err)
		}
		fmt.Fprintf(h, "%s:%s\n", p, fh)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GlobFiles returns all files matching the pattern relative to baseDir.
// Supports *, ** and standard shell glob patterns.
func GlobFiles(baseDir, pattern string) ([]string, error) {
	// Handle absolute patterns.
	if filepath.IsAbs(pattern) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		return matches, nil
	}

	// Handle double-star glob manually.
	if strings.Contains(pattern, "**") {
		return doubleStarGlob(baseDir, pattern)
	}

	matches, err := filepath.Glob(filepath.Join(baseDir, pattern))
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func doubleStarGlob(baseDir, pattern string) ([]string, error) {
	// Split pattern on **.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	searchRoot := filepath.Join(baseDir, prefix)
	var results []string

	err := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(searchRoot, path)
		if err != nil {
			return err
		}
		if suffix == "" {
			results = append(results, path)
			return nil
		}
		matched, err := filepath.Match(suffix, filepath.Base(rel))
		if err != nil {
			return err
		}
		if matched {
			results = append(results, path)
		}
		return nil
	})
	return results, err
}

// SplitImageRef splits an image reference into (name, tag).
// Handles registries with ports, e.g. "localhost:5000/myapp:dev" → ("localhost:5000/myapp", "dev").
// Defaults tag to "latest" when not specified.
func SplitImageRef(ref string) (string, string) {
	// Find the last '/' to isolate the final path component.
	lastSlash := strings.LastIndex(ref, "/")
	var prefix, last string
	if lastSlash >= 0 {
		prefix = ref[:lastSlash+1]
		last = ref[lastSlash+1:]
	} else {
		prefix = ""
		last = ref
	}

	// Split the final component on ':'.
	if idx := strings.LastIndex(last, ":"); idx >= 0 {
		return prefix + last[:idx], last[idx+1:]
	}
	return prefix + last, "latest"
}

// ImageKey returns the filesystem-safe key for an image name+tag.
func ImageKey(name, tag string) string {
	safe := strings.ReplaceAll(name, "/", "_")
	return safe + "_" + tag
}
