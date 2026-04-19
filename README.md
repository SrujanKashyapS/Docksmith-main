# Docksmith

A simplified Docker-like container system implemented in Go, featuring a build system with deterministic layer caching, process isolation via chroot, and a complete CLI.

## Project Structure

```
.
├── cmd/docksmith/     # CLI entry point
├── build/             # Docksmithfile parser, builder, cache
├── image/             # Image manifest I/O and layer management
├── runtime/           # Container runtime (chroot isolation)
├── utils/             # Shared utilities (tar, SHA256, glob)
└── sample/            # Sample application
```

## State Storage

All state is stored in `~/.docksmith/`:

```
~/.docksmith/
├── images/    # Image manifests (.json)
├── layers/    # Immutable layer tarballs (named by SHA256 digest)
└── cache/     # Build cache entries (cachekey → layer digest)
```

## CLI Commands

```
docksmith build -t <name:tag> [--no-cache] <context-dir>
docksmith run [-e KEY=VALUE]... <name:tag> [cmd [args...]]
docksmith images
docksmith rmi <name:tag>
docksmith import <dir> <name:tag>
```

## Docksmithfile Syntax

```dockerfile
FROM <image>:<tag>          # Load base image (or "scratch")
WORKDIR <path>              # Set working directory (no layer)
ENV key=value               # Set environment variable (no layer)
COPY <src> <dest>           # Copy files into a layer (supports glob, **)
RUN <command>               # Execute command in isolated chroot (creates layer)
CMD ["exec", "arg"]         # Default container command (JSON array)
```

## Layer System

- `COPY` and `RUN` produce immutable layers stored as tar archives
- Each layer is named by its SHA256 digest
- Layers are extracted in order to form the container filesystem

## Cache System

The cache key includes:
- Previous layer digest
- Full instruction text
- WORKDIR value
- Sorted ENV values
- For COPY: hash of source files

Rules:
- Cache hit → reuse layer, print `[CACHE HIT]`
- Cache miss → execute, store layer, print `[CACHE MISS]`
- First miss causes all subsequent steps to be `CACHE MISS`
- Use `--no-cache` to disable caching

## Container Runtime

The `run` command:
1. Extracts all image layers into a temporary directory
2. Sets environment variables
3. Sets the working directory
4. Executes the command inside a `chroot` (same isolation as `RUN` during build)

> **Note**: The `run` command and `RUN` instruction require root privileges (or `CAP_SYS_CHROOT`) to perform the chroot.

## Quick Start

### 1. Build docksmith

```bash
go build -o docksmith ./cmd/docksmith
```

### 2. Bootstrap a base image

```bash
sudo bash sample/setup.sh
```

This creates a minimal busybox rootfs and imports it as `busybox:latest`.

### 3. Build the sample app

```bash
sudo ./docksmith build -t hello:latest sample/
```

### 4. Run the sample app

```bash
sudo ./docksmith run hello:latest
```

### 5. Test the cache

```bash
# Second build uses cached layers:
sudo ./docksmith build -t hello:latest sample/

# Force rebuild:
sudo ./docksmith build -t hello:latest --no-cache sample/
```

### 6. Override ENV at runtime

```bash
sudo ./docksmith run -e APP_NAME=myapp -e VERSION=2.0 hello:latest
```

### 7. List images

```bash
./docksmith images
```

### 8. Remove an image

```bash
./docksmith rmi hello:latest
```

## Manifest Format

```json
{
  "name": "hello",
  "tag": "latest",
  "digest": "sha256:...",
  "created": "2024-01-01T00:00:00Z",
  "config": {
    "env": ["APP_NAME=docksmith-hello", "VERSION=1.0.0"],
    "cmd": ["/bin/sh", "/app/hello.sh"],
    "workingDir": "/app"
  },
  "layers": [
    {
      "digest": "abc123...",
      "size": 4096,
      "createdBy": "COPY hello.sh /app/hello.sh"
    }
  ]
}
```

## Building and Testing

```bash
# Build
go build ./cmd/docksmith

# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...
```
