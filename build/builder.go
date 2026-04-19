package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/SrujanKashyapS/Docksmith/image"
	"github.com/SrujanKashyapS/Docksmith/utils"
)

// BuildOptions configures a build.
type BuildOptions struct {
	ContextDir string
	ImageName  string
	ImageTag   string
	NoCache    bool
}

// Builder orchestrates a Docksmithfile build.
type Builder struct {
	opts BuildOptions

	// Accumulated image state.
	layers     []image.LayerInfo
	config     image.Config
	prevDigest string // digest of the last produced layer

	// Cache state.
	cacheInvalid bool // true after first CACHE MISS
}

// Build parses and executes a Docksmithfile, producing an image.
func Build(opts BuildOptions, docksmithfileContent string) error {
	if err := utils.EnsureDirs(); err != nil {
		return fmt.Errorf("ensuring dirs: %w", err)
	}

	instructions, err := ParseDocksmithfile(docksmithfileContent)
	if err != nil {
		return fmt.Errorf("parsing Docksmithfile: %w", err)
	}

	b := &Builder{
		opts: opts,
		config: image.Config{
			WorkingDir: "/",
		},
	}

	for i, instr := range instructions {
		fmt.Printf("Step %d/%d: %s\n", i+1, len(instructions), instr.Raw)
		if err := b.execute(instr); err != nil {
			return fmt.Errorf("step %d (%s): %w", i+1, instr.Raw, err)
		}
	}

	createdTime := time.Now().UTC()
	if !b.cacheInvalid {
		if existing, err := image.Load(opts.ImageName, opts.ImageTag); err == nil {
			createdTime = existing.Created
		}
	}

	// Save manifest.
	m := &image.Manifest{
		Name:    opts.ImageName,
		Tag:     opts.ImageTag,
		Created: createdTime,
		Config:  b.config,
		Layers:  b.layers,
	}
	if err := m.Save(); err != nil {
		return fmt.Errorf("saving manifest: %w", err)
	}
	fmt.Printf("Successfully built %s:%s\n", opts.ImageName, opts.ImageTag)
	return nil
}

// execute processes a single instruction.
func (b *Builder) execute(instr Instruction) error {
	switch instr.Type {
	case InstrFROM:
		return b.execFROM(instr)
	case InstrCOPY:
		return b.execCOPY(instr)
	case InstrRUN:
		return b.execRUN(instr)
	case InstrWORKDIR:
		return b.execWORKDIR(instr)
	case InstrENV:
		return b.execENV(instr)
	case InstrCMD:
		return b.execCMD(instr)
	default:
		return fmt.Errorf("unknown instruction: %s", instr.Type)
	}
}

func (b *Builder) execFROM(instr Instruction) error {
	ref := instr.Args[0]
	name, tag := utils.SplitImageRef(ref)

	if strings.ToLower(ref) == "scratch" {
		// scratch: empty base image
		b.layers = nil
		b.config = image.Config{WorkingDir: "/"}
		b.prevDigest = ""
		fmt.Println(" ---> Using scratch base")
		return nil
	}

	m, err := image.Load(name, tag)
	if err != nil {
		return err
	}

	b.layers = make([]image.LayerInfo, len(m.Layers))
	copy(b.layers, m.Layers)
	b.config = m.Config
	b.prevDigest = m.Digest // Base image's manifest digest for cache invalidation
	fmt.Printf(" ---> Loaded %s:%s (%d layers)\n", name, tag, len(b.layers))
	return nil
}

func (b *Builder) execCOPY(instr Instruction) error {
	args := instr.Args
	if len(args) < 2 {
		return fmt.Errorf("COPY requires at least <src> <dest>")
	}
	// Last arg is dest, everything before is src.
	dest := args[len(args)-1]
	srcs := args[:len(args)-1]

	// Compute source file hash for cache key.
	var srcHashes []string
	for _, src := range srcs {
		h, err := HashSourceFiles(b.opts.ContextDir, src)
		if err != nil {
			return fmt.Errorf("hashing sources: %w", err)
		}
		srcHashes = append(srcHashes, h)
	}
	srcHash := utils.SHA256String(strings.Join(srcHashes, "|"))

	// Check cache.
	cacheKey := CacheKey(b.prevDigest, instr.Raw, b.config.WorkingDir, b.config.Env, srcHash)
	if digest, ok, err := b.checkCache(cacheKey); err != nil {
		return err
	} else if ok {
		b.addLayer(digest, instr.Raw)
		return nil
	}

	// Resolve dest relative to WORKDIR if not absolute.
	resolvedDest := resolvePath(b.config.WorkingDir, dest)

	// Build tar for each src and merge.
	var combinedTar []byte
	for _, src := range srcs {
		tarBytes, err := image.CreateCopyLayer(b.opts.ContextDir, src, resolvedDest)
		if err != nil {
			return err
		}
		combinedTar = append(combinedTar, tarBytes...)
	}

	digest, err := image.StoreLayer(combinedTar)
	if err != nil {
		return err
	}

	if !b.opts.NoCache {
		if err := StoreCache(cacheKey, digest); err != nil {
			return fmt.Errorf("storing cache: %w", err)
		}
	}
	b.addLayer(digest, instr.Raw)
	return nil
}

func (b *Builder) execRUN(instr Instruction) error {
	command := instr.Args[0]

	// Check cache.
	cacheKey := CacheKey(b.prevDigest, instr.Raw, b.config.WorkingDir, b.config.Env, "")
	if digest, ok, err := b.checkCache(cacheKey); err != nil {
		return err
	} else if ok {
		b.addLayer(digest, instr.Raw)
		return nil
	}

	// Create a temp directory to hold the extracted filesystem.
	tmpDir, err := os.MkdirTemp("", "docksmith-run-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Extract all current layers.
	var digests []string
	for _, l := range b.layers {
		digests = append(digests, l.Digest)
	}
	if err := image.ExtractLayers(digests, tmpDir); err != nil {
		return fmt.Errorf("extracting layers: %w", err)
	}

	// Ensure basic directories exist in the chroot.
	ensureChrootDirs(tmpDir)

	// Resolve working directory.
	workdir := resolvePath("/", b.config.WorkingDir)
	absWorkdir := filepath.Join(tmpDir, workdir)
	if err := os.MkdirAll(absWorkdir, 0o755); err != nil {
		return fmt.Errorf("creating workdir: %w", err)
	}

	// Snapshot before running.
	snapshot, err := utils.ScanDir(tmpDir)
	if err != nil {
		return fmt.Errorf("snapshotting: %w", err)
	}

	// Run command in chroot.
	if err := runInChroot(tmpDir, workdir, command, b.config.Env); err != nil {
		return fmt.Errorf("RUN %q failed: %w", command, err)
	}

	// Create delta layer.
	tarBytes, err := image.CreateDeltaLayer(tmpDir, snapshot)
	if err != nil {
		return fmt.Errorf("creating delta layer: %w", err)
	}

	digest, err := image.StoreLayer(tarBytes)
	if err != nil {
		return err
	}

	if !b.opts.NoCache {
		if err := StoreCache(cacheKey, digest); err != nil {
			return fmt.Errorf("storing cache: %w", err)
		}
	}
	b.addLayer(digest, instr.Raw)
	return nil
}

func (b *Builder) execWORKDIR(instr Instruction) error {
	path := instr.Args[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.config.WorkingDir, path)
	}
	b.config.WorkingDir = filepath.Clean(path)
	fmt.Printf(" ---> WORKDIR = %s\n", b.config.WorkingDir)
	return nil
}

func (b *Builder) execENV(instr Instruction) error {
	kv := instr.Args[0]
	// Remove existing key if present.
	parts := strings.SplitN(kv, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("ENV requires key=value format")
	}
	key := parts[0]
	// Remove existing entry with same key.
	var newEnv []string
	for _, e := range b.config.Env {
		if !strings.HasPrefix(e, key+"=") {
			newEnv = append(newEnv, e)
		}
	}
	newEnv = append(newEnv, kv)
	sort.Strings(newEnv)
	b.config.Env = newEnv
	fmt.Printf(" ---> ENV %s\n", kv)
	return nil
}

func (b *Builder) execCMD(instr Instruction) error {
	b.config.Cmd = instr.Args
	fmt.Printf(" ---> CMD %v\n", instr.Args)
	return nil
}

// checkCache checks the cache for the given key and prints hit/miss.
func (b *Builder) checkCache(key string) (string, bool, error) {
	if b.opts.NoCache || b.cacheInvalid {
		fmt.Println(" ---> [CACHE MISS]")
		b.cacheInvalid = true
		return "", false, nil
	}
	digest, hit, err := LookupCache(key)
	if err != nil {
		return "", false, err
	}
	if hit {
		fmt.Println(" ---> [CACHE HIT]")
		return digest, true, nil
	}
	fmt.Println(" ---> [CACHE MISS]")
	b.cacheInvalid = true
	return "", false, nil
}

// addLayer appends a layer to the accumulated layer list.
func (b *Builder) addLayer(digest, createdBy string) {
	size, _ := image.LayerSize(digest)
	b.layers = append(b.layers, image.LayerInfo{
		Digest:    digest,
		Size:      size,
		CreatedBy: createdBy,
	})
	b.prevDigest = digest
}

// resolvePath resolves a path relative to workdir.
func resolvePath(workdir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workdir, path))
}

// ensureChrootDirs creates the minimal directory structure needed for chroot.
func ensureChrootDirs(root string) {
	dirs := []string{
		"bin", "etc", "dev", "proc", "sys",
		"tmp", "usr/bin", "usr/lib", "lib",
		"lib64", "var", "root", "home",
	}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
}

// runInChroot runs a shell command inside a chroot environment.
// This requires CAP_SYS_CHROOT (i.e., running as root).
func runInChroot(rootDir, workdir, command string, envVars []string) error {
	// Build the full env: start with a minimal base, then add image envs.
	env := buildEnv(envVars)

	// Use /bin/sh -c to run the command inside the chroot.
	// SysProcAttr.Chroot performs the chroot before execing the process.
	shell := findShell(rootDir)

	cmd := exec.Command(shell, "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Chroot: rootDir,
	}
	cmd.Dir = workdir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// findShell returns the path to a usable shell inside the chroot.
// It checks common shell locations.
func findShell(rootDir string) string {
	candidates := []string{"/bin/sh", "/bin/bash", "/usr/bin/sh", "/usr/bin/bash"}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(rootDir, c)); err == nil {
			return c
		}
	}
	// Fall back to /bin/sh — the chroot must have it.
	return "/bin/sh"
}

// buildEnv builds the environment variable slice for a chroot command.
func buildEnv(imageEnvs []string) []string {
	base := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm",
	}
	// Merge image envs (they override base).
	merged := make(map[string]string)
	for _, e := range base {
		parts := strings.SplitN(e, "=", 2)
		merged[parts[0]] = parts[1]
	}
	for _, e := range imageEnvs {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			merged[parts[0]] = parts[1]
		}
	}
	var result []string
	for k, v := range merged {
		result = append(result, k+"="+v)
	}
	sort.Strings(result)
	return result
}
