package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/SrujanKashyapS/Docksmith/image"
	"github.com/SrujanKashyapS/Docksmith/utils"
)

// RunOptions configures a container run.
type RunOptions struct {
	ImageName string
	ImageTag  string
	Cmd       []string   // override CMD if non-empty
	Env       []string   // additional env vars (KEY=VALUE), override image envs
	Remove    bool       // remove rootfs after exit
}

// Run executes a container from an image.
func Run(opts RunOptions) error {
	if err := utils.EnsureDirs(); err != nil {
		return fmt.Errorf("ensuring dirs: %w", err)
	}

	// Load image manifest.
	m, err := image.Load(opts.ImageName, opts.ImageTag)
	if err != nil {
		return err
	}

	// Create temp rootfs directory.
	tmpDir, err := os.MkdirTemp("", "docksmith-rootfs-*")
	if err != nil {
		return fmt.Errorf("creating rootfs: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("Extracting image %s:%s...\n", opts.ImageName, opts.ImageTag)

	// Extract all layers.
	if err := image.ExtractManifestLayers(m, tmpDir); err != nil {
		return fmt.Errorf("extracting layers: %w", err)
	}

	// Ensure minimal dirs exist.
	ensureRootfsDirs(tmpDir)

	// Determine command to run.
	cmd := m.Config.Cmd
	if len(opts.Cmd) > 0 {
		cmd = opts.Cmd
	}
	if len(cmd) == 0 {
		return fmt.Errorf("no command specified and image has no CMD")
	}

	// Build environment.
	env := mergeEnvs(m.Config.Env, opts.Env)

	// Determine working directory.
	workdir := m.Config.WorkingDir
	if workdir == "" {
		workdir = "/"
	}

	// Ensure workdir exists in rootfs.
	absWorkdir := filepath.Join(tmpDir, workdir)
	if err := os.MkdirAll(absWorkdir, 0o755); err != nil {
		return fmt.Errorf("creating workdir %s: %w", workdir, err)
	}

	fmt.Printf("Running: %s\n", strings.Join(cmd, " "))
	fmt.Println("---")

	// Execute inside chroot.
	return runIsolated(tmpDir, workdir, cmd, env)
}

// runIsolated runs a command inside a chroot.
// Requires CAP_SYS_CHROOT (i.e., running as root).
func runIsolated(rootDir, workdir string, cmd []string, env []string) error {
	if len(cmd) == 0 {
		return fmt.Errorf("empty command")
	}

	// Resolve binary path inside the chroot.
	binary := cmd[0]

	c := exec.Command(binary, cmd[1:]...)
	c.SysProcAttr = &syscall.SysProcAttr{
		Chroot: rootDir,
	}
	c.Dir = workdir
	c.Env = env
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		// Don't wrap exit errors — let them propagate naturally.
		return err
	}
	return nil
}

// ensureRootfsDirs creates the minimal directory structure required for a rootfs.
func ensureRootfsDirs(root string) {
	dirs := []string{
		"bin", "etc", "dev", "proc", "sys",
		"tmp", "usr/bin", "usr/lib", "lib",
		"lib64", "var/tmp", "root", "home",
	}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	// Set /tmp permissions.
	os.Chmod(filepath.Join(root, "tmp"), 0o1777)
}

// mergeEnvs merges base envs with override envs.
// Override envs take precedence.
func mergeEnvs(base, overrides []string) []string {
	merged := make(map[string]string)

	// Minimal system defaults.
	defaults := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm",
	}
	for _, e := range defaults {
		parts := strings.SplitN(e, "=", 2)
		merged[parts[0]] = parts[1]
	}

	// Image envs override defaults.
	for _, e := range base {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			merged[parts[0]] = parts[1]
		}
	}

	// Runtime -e overrides override image envs.
	for _, e := range overrides {
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
