#!/bin/bash
# setup.sh — Bootstrap a busybox base image for Docksmith
#
# This script creates a minimal busybox-based rootfs and imports it
# as busybox:latest into the Docksmith local image store.
#
# Requirements:
#   - Run as root (needed for chroot isolation)
#   - busybox must be installed on the host (apt-get install busybox-static)
#     OR debootstrap / any other rootfs creation method
#
# Usage:
#   sudo bash setup.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOTFS_DIR="/tmp/docksmith-busybox-rootfs"
DOCKSMITH="${SCRIPT_DIR}/../docksmith"

# Build the docksmith binary if needed.
if [ ! -f "$DOCKSMITH" ]; then
    echo "Building docksmith binary..."
    cd "$SCRIPT_DIR/.."
    go build -o docksmith ./cmd/docksmith
    cd "$SCRIPT_DIR"
fi

echo "=== Creating minimal busybox rootfs at $ROOTFS_DIR ==="
rm -rf "$ROOTFS_DIR"
mkdir -p "$ROOTFS_DIR"/{bin,etc,tmp,dev,proc,sys,usr/bin,lib,lib64,var/tmp,root,home,app}

# Find busybox binary.
BUSYBOX=""
for candidate in /bin/busybox /usr/bin/busybox /usr/local/bin/busybox; do
    if [ -x "$candidate" ]; then
        BUSYBOX="$candidate"
        break
    fi
done

if [ -z "$BUSYBOX" ]; then
    # Try to install busybox-static.
    echo "busybox not found. Attempting to install busybox-static..."
    if command -v apt-get &>/dev/null; then
        apt-get install -y busybox-static 2>/dev/null || true
        BUSYBOX="/bin/busybox"
    fi
fi

if [ -z "$BUSYBOX" ] || [ ! -x "$BUSYBOX" ]; then
    echo "ERROR: busybox not found. Please install busybox-static:"
    echo "  sudo apt-get install busybox-static"
    exit 1
fi

echo "Using busybox: $BUSYBOX"
cp "$BUSYBOX" "$ROOTFS_DIR/bin/busybox"
chmod +x "$ROOTFS_DIR/bin/busybox"

# Create symlinks for common commands.
COMMANDS="sh bash echo cat ls pwd mkdir cp mv rm chmod touch grep sed awk"
for cmd in $COMMANDS; do
    if ! [ -f "$ROOTFS_DIR/bin/$cmd" ]; then
        ln -sf busybox "$ROOTFS_DIR/bin/$cmd" 2>/dev/null || true
    fi
done

# Create /etc/passwd and /etc/group.
cat > "$ROOTFS_DIR/etc/passwd" <<'EOF'
root:x:0:0:root:/root:/bin/sh
nobody:x:65534:65534:nobody:/nonexistent:/bin/sh
EOF

cat > "$ROOTFS_DIR/etc/group" <<'EOF'
root:x:0:
nobody:x:65534:
EOF

# Also copy /bin/sh from host as fallback if busybox sh doesn't work.
if [ -f /bin/sh ] && [ ! -f "$ROOTFS_DIR/bin/sh" ]; then
    cp /bin/sh "$ROOTFS_DIR/bin/sh" || true
fi

# Copy required shared libraries for the shell if needed.
copy_libs() {
    local binary="$1"
    local dest_dir="$2"
    if command -v ldd &>/dev/null; then
        ldd "$binary" 2>/dev/null | grep -oP '(/lib|/usr/lib)[^ ]+' | while read lib; do
            if [ -f "$lib" ]; then
                dest_lib="$dest_dir$(dirname $lib)"
                mkdir -p "$dest_lib"
                cp -n "$lib" "$dest_lib/" 2>/dev/null || true
            fi
        done
    fi
}

# Copy libs for busybox.
copy_libs "$BUSYBOX" "$ROOTFS_DIR"

# Set permissions.
chmod 1777 "$ROOTFS_DIR/tmp"
chmod 1777 "$ROOTFS_DIR/var/tmp"

echo "=== Importing rootfs as busybox:latest ==="
"$DOCKSMITH" import "$ROOTFS_DIR" busybox:latest

echo ""
echo "=== Setup complete! ==="
echo ""
echo "Now you can build and run the sample:"
echo "  sudo $DOCKSMITH build -t hello:latest $SCRIPT_DIR"
echo "  sudo $DOCKSMITH run hello:latest"
echo ""
echo "Test the cache:"
echo "  sudo $DOCKSMITH build -t hello:latest $SCRIPT_DIR   # should show CACHE HIT"
echo "  sudo $DOCKSMITH build -t hello:latest --no-cache $SCRIPT_DIR  # force rebuild"
echo ""
echo "Override ENV at runtime:"
echo "  sudo $DOCKSMITH run -e APP_NAME=custom hello:latest"
