package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/SrujanKashyapS/Docksmith/build"
	"github.com/SrujanKashyapS/Docksmith/image"
	"github.com/SrujanKashyapS/Docksmith/runtime"
	"github.com/SrujanKashyapS/Docksmith/utils"
)

const usage = `Docksmith - A simplified Docker-like container system

Usage:
  docksmith build -t <name:tag> [--no-cache] <context-dir>
  docksmith run [-e KEY=VALUE]... <name:tag> [cmd [args...]]
  docksmith images
  docksmith rmi <name:tag>
  docksmith import <dir> <name:tag>

Commands:
  build   Build an image from a Docksmithfile in <context-dir>
  run     Run a container from an image
  images  List locally stored images
  rmi     Remove a locally stored image
  import  Import a directory as a base image (useful for bootstrapping)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "build":
		err = cmdBuild(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "images":
		err = cmdImages()
	case "rmi":
		err = cmdRmi(os.Args[2:])
	case "import":
		err = cmdImport(os.Args[2:])
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// cmdBuild handles: docksmith build -t name:tag [--no-cache] <context>
func cmdBuild(args []string) error {
	var imageRef string
	var noCache bool
	var contextDir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-t", "--tag":
			if i+1 >= len(args) {
				return fmt.Errorf("-t requires an argument")
			}
			i++
			imageRef = args[i]
		case "--no-cache":
			noCache = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
			contextDir = args[i]
		}
	}

	if imageRef == "" {
		return fmt.Errorf("build requires -t <name:tag>")
	}
	if contextDir == "" {
		return fmt.Errorf("build requires a context directory")
	}

	// Resolve context directory to absolute path.
	absContext, err := filepath.Abs(contextDir)
	if err != nil {
		return fmt.Errorf("resolving context: %w", err)
	}

	// Read Docksmithfile.
	docksmithfilePath := filepath.Join(absContext, "Docksmithfile")
	content, err := os.ReadFile(docksmithfilePath)
	if err != nil {
		return fmt.Errorf("reading Docksmithfile: %w", err)
	}

	name, tag := utils.SplitImageRef(imageRef)

	opts := build.BuildOptions{
		ContextDir: absContext,
		ImageName:  name,
		ImageTag:   tag,
		NoCache:    noCache,
	}

	return build.Build(opts, string(content))
}

// cmdRun handles: docksmith run [-e K=V]... name:tag [cmd [args...]]
func cmdRun(args []string) error {
	var imageRef string
	var extraEnv []string
	var overrideCmd []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-e", "--env":
			if i+1 >= len(args) {
				return fmt.Errorf("-e requires an argument")
			}
			i++
			extraEnv = append(extraEnv, args[i])
		default:
			if strings.HasPrefix(args[i], "-e") && len(args[i]) > 2 {
				extraEnv = append(extraEnv, args[i][2:])
				continue
			}
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
			if imageRef == "" {
				imageRef = args[i]
			} else {
				overrideCmd = append(overrideCmd, args[i:]...)
				break
			}
		}
	}

	if imageRef == "" {
		return fmt.Errorf("run requires <name:tag>")
	}

	name, tag := utils.SplitImageRef(imageRef)

	opts := runtime.RunOptions{
		ImageName: name,
		ImageTag:  tag,
		Cmd:       overrideCmd,
		Env:       extraEnv,
		Remove:    true,
	}

	return runtime.Run(opts)
}

// cmdImages handles: docksmith images
func cmdImages() error {
	if err := utils.EnsureDirs(); err != nil {
		return err
	}
	manifests, err := image.ListAll()
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tTAG\tDIGEST\tCREATED\tSIZE")
	for _, m := range manifests {
		shortDigest := m.Digest
		if len(shortDigest) > 19 {
			shortDigest = shortDigest[:19]
		}
		var totalSize int64
		for _, l := range m.Layers {
			totalSize += l.Size
		}
		created := m.Created.Format("2006-01-02 15:04:05")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			m.Name, m.Tag, shortDigest, created, formatSize(totalSize))
	}
	return w.Flush()
}

// cmdRmi handles: docksmith rmi name:tag
func cmdRmi(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("rmi requires <name:tag>")
	}
	for _, ref := range args {
		name, tag := utils.SplitImageRef(ref)
		if err := image.Delete(name, tag); err != nil {
			return err
		}
		fmt.Printf("Deleted: %s:%s\n", name, tag)
	}
	return nil
}

// cmdImport handles: docksmith import <dir> <name:tag>
// Creates a base image from a local directory (useful for bootstrapping).
func cmdImport(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("import requires <dir> <name:tag>")
	}
	srcDir := args[0]
	imageRef := args[1]

	absDir, err := filepath.Abs(srcDir)
	if err != nil {
		return fmt.Errorf("resolving dir: %w", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", absDir)
	}

	if err := utils.EnsureDirs(); err != nil {
		return err
	}

	fmt.Printf("Importing %s as %s...\n", absDir, imageRef)

	// Create a tar of the directory.
	tarBytes, err := createImportTar(absDir)
	if err != nil {
		return fmt.Errorf("creating tar: %w", err)
	}

	digest, err := image.StoreLayer(tarBytes)
	if err != nil {
		return fmt.Errorf("storing layer: %w", err)
	}

	size, _ := image.LayerSize(digest)

	name, tag := utils.SplitImageRef(imageRef)
	m := &image.Manifest{
		Name:    name,
		Tag:     tag,
		Created: time.Now().UTC(),
		Config: image.Config{
			Env:        []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			Cmd:        []string{"/bin/sh"},
			WorkingDir: "/",
		},
		Layers: []image.LayerInfo{
			{
				Digest:    digest,
				Size:      size,
				CreatedBy: "import " + absDir,
			},
		},
	}
	if err := m.Save(); err != nil {
		return fmt.Errorf("saving manifest: %w", err)
	}

	fmt.Printf("Imported %s:%s (layer: %s, size: %s)\n",
		name, tag, digest[:12], formatSize(size))
	return nil
}

// createImportTar creates a deterministic tar archive of a directory,
// suitable for use as a base image layer.
func createImportTar(srcDir string) ([]byte, error) {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)
	errCh := make(chan error, 1)

	go func() {
		defer func() {
			tw.Close()
			pw.Close()
		}()
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

			var hdr *tar.Header
			if info.Mode()&os.ModeSymlink != 0 {
				link, _ := os.Readlink(path)
				hdr = &tar.Header{
					Typeflag: tar.TypeSymlink,
					Name:     filepath.ToSlash(rel),
					Linkname: link,
				}
			} else {
				hdr, err = tar.FileInfoHeader(info, "")
				if err != nil {
					return err
				}
				hdr.Name = filepath.ToSlash(rel)
				if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
					hdr.Name += "/"
				}
			}
			// Zero timestamps.
			hdr.ModTime = time.Time{}
			hdr.AccessTime = time.Time{}
			hdr.ChangeTime = time.Time{}
			hdr.Uid = 0
			hdr.Gid = 0
			hdr.Uname = ""
			hdr.Gname = ""

			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(tw, f)
				f.Close()
				return copyErr
			}
			return nil
		})
		errCh <- err
	}()

	data, readErr := io.ReadAll(pr)
	writeErr := <-errCh
	if writeErr != nil {
		return nil, writeErr
	}
	return data, readErr
}

// formatSize formats a byte count as a human-readable string.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
