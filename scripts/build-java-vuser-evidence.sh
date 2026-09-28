#!/usr/bin/env bash
set -euo pipefail
# Offline-capable after Maven's dependency cache is populated. No user scripts
# are compiled by the production runner; this only builds repository fixtures.
repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
fixture_dir="$repo_dir/runners/java_vuser/evidence"
output_dir="$repo_dir/bin/java_vuser/evidence"
make -C "$repo_dir" java-vuser-runner
# These directories contain only this script's generated output. Starting clean
# prevents an old shim or a stale dependency version entering the fixture JAR.
rm -rf -- "$output_dir/classes" "$output_dir/lib"
mkdir -p "$output_dir/classes" "$output_dir/lib"
mvn -q -f "$fixture_dir/pom.xml" org.apache.maven.plugins:maven-dependency-plugin:3.6.1:copy-dependencies "-DoutputDirectory=$output_dir/lib"
fixture_classpath="$repo_dir/bin/java_vuser/apexload-java-vuser-runner.jar"
for dependency in "$output_dir"/lib/*.jar; do
  fixture_classpath="$fixture_classpath:$dependency"
done
javac --release 8 -cp "$fixture_classpath" -d "$output_dir/classes" "$fixture_dir/LightActions.java" "$fixture_dir/HeavyActions.java"
jar --create --file "$output_dir/fixtures.jar" -C "$output_dir/classes" .
