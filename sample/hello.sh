#!/bin/sh
# hello.sh — Sample application for Docksmith
# This script prints a greeting and information about the environment.

echo "========================================"
echo "  Hello from Docksmith!"
echo "========================================"
echo ""
echo "Container environment:"
echo "  WORKDIR : $(pwd)"
echo "  APP_NAME: ${APP_NAME:-not set}"
echo "  VERSION : ${VERSION:-not set}"
echo ""
echo "Docksmith is a simplified Docker-like system"
echo "built in Go, demonstrating:"
echo "  - FROM     : loading a base image"
echo "  - COPY     : copying files into layers"
echo "  - RUN      : executing commands in isolation"
echo "  - WORKDIR  : setting the working directory"
echo "  - ENV      : injecting environment variables"
echo "  - CMD      : default container command"
echo ""
echo "Build complete. Goodbye!"
