#!/bin/sh
set -eu
tray_arch="${1:-arm64}"
tray_output="${2:-bin/keepassxc-agent-tray}"
case "$tray_arch" in arm64|x86_64) ;; amd64) tray_arch=x86_64 ;; *) echo "unsupported macOS architecture" >&2; exit 1 ;; esac
mkdir -p "$(dirname "$tray_output")"
swiftc -module-cache-path /private/tmp/keepassxc-agent-swift-modules -swift-version 5 -O -target "$tray_arch-apple-macosx12.0" -framework AppKit native/macos/Tray.swift -o "$tray_output"
