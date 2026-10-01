#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output=${1:-"$root/../../bin/apexload-monitor-helper.jar"}
classes=$(mktemp -d)
trap 'rm -rf "$classes"' EXIT HUP INT TERM
javac --release 17 -d "$classes" "$root/ApexMonitor.java"
mkdir -p "$(dirname -- "$output")"
jar --create --file "$output" -C "$classes" .
