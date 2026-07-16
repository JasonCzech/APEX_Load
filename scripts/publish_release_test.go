package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fakeGitHubCLI = `#!/usr/bin/env bash
set -euo pipefail

state_dir="${FAKE_GH_STATE_DIR:?}"
{
  first=true
  for argument in "$@"; do
    if [[ "$first" == true ]]; then
      printf '%s' "$argument"
      first=false
    else
      printf '\t%s' "$argument"
    fi
  done
  printf '\n'
} >> "$state_dir/calls.tsv"

[[ "${1:-}" == release ]] || {
  printf 'fake gh: expected release command\n' >&2
  exit 90
}
subcommand="${2:-}"
shift 2

case "$subcommand" in
  view)
    tag="${1:?}"
    draft="$(<"$state_dir/draft")"
    body="$(<"$state_dir/body")"
    names='[]'
    shopt -s nullglob
    for asset in "$state_dir/remote"/*; do
      name="${asset##*/}"
      names="$(jq -c --arg name "$name" '. + [$name]' <<< "$names")"
    done
    jq -n -c \
      --argjson draft "$draft" \
      --arg tag "$tag" \
      --arg body "$body" \
      --argjson names "$names" \
      '{isDraft:$draft, tagName:$tag, body:$body, assets:($names | map({name:.}))}'
    ;;
  download)
    tag="${1:?}"
    shift
    destination=''
    while (( $# > 0 )); do
      case "$1" in
        --dir)
          destination="${2:?}"
          shift 2
          ;;
        --repo)
          shift 2
          ;;
        *)
          printf 'fake gh: unexpected download argument %s\n' "$1" >&2
          exit 91
          ;;
      esac
    done
    [[ -n "$destination" ]] || exit 92
    mkdir -p "$destination"
    shopt -s nullglob
    for asset in "$state_dir/remote"/*; do
      cp "$asset" "$destination/"
    done
    ;;
  upload)
    tag="${1:?}"
    asset="${2:?}"
    [[ -f "$asset" ]] || exit 93
    cp "$asset" "$state_dir/remote/${asset##*/}"
    ;;
  edit)
    [[ "${1:-}" != '' ]] || exit 94
    found=false
    for argument in "$@"; do
      if [[ "$argument" == --draft=false ]]; then
        found=true
      fi
    done
    [[ "$found" == true ]] || exit 95
    printf 'false\n' > "$state_dir/draft"
    ;;
  *)
    printf 'fake gh: unexpected release subcommand %s\n' "$subcommand" >&2
    exit 96
    ;;
esac
`

const fakeReleaseVerifier = `#!/usr/bin/env bash
set -euo pipefail
state_dir="${FAKE_GH_STATE_DIR:?}"
printf '%s\t%s\t%s\n' "${1:?}" "${2:?}" "${3:?}" >> "$state_dir/verifier-calls.tsv"
`

const portableFind = `#!/usr/bin/env bash
set -euo pipefail
directory="${1:?}"
[[ "${2:-}" == -mindepth && "${3:-}" == 1 && "${4:-}" == -maxdepth && "${5:-}" == 1 && "${6:-}" == -type && "${7:-}" == f && "${8:-}" == -printf && "${9:-}" == '%f\n' ]] || {
  printf 'fake find: unexpected arguments\n' >&2
  exit 97
}
shopt -s nullglob dotglob
for candidate in "$directory"/*; do
  [[ -f "$candidate" ]] && printf '%s\n' "${candidate##*/}"
done
`

type publishReleaseFixture struct {
	releaseDir string
	publicKey  string
	verifier   string
	stateDir   string
	toolDir    string
	tag        string
	commit     string
	repository string
}

func TestPublishReleaseUploadsEmptyAuthenticatedDraftAndPublishes(t *testing.T) {
	fixture := newPublishReleaseFixture(t, true, map[string]string{
		"SHA256SUMS":            "signed checksums\n",
		"release-manifest.json": "{\"schema_version\":1}\n",
	}, nil)

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("publish empty draft: %v\n%s", err, output)
	}
	if !strings.Contains(output, "published byte-verified immutable release "+fixture.tag) {
		t.Fatalf("publication success was not reported:\n%s", output)
	}
	fixture.assertPublished(t, true)
	fixture.assertRemoteMatchesLocal(t)
	fixture.assertUploaded(t, []string{"SHA256SUMS", "release-manifest.json"})
	fixture.assertVerifierCallCount(t, 2)
}

func TestPublishReleaseResumesMatchingPartialDraft(t *testing.T) {
	local := map[string]string{
		"SHA256SUMS":            "signed checksums\n",
		"release-manifest.json": "{\"schema_version\":1}\n",
	}
	fixture := newPublishReleaseFixture(t, true, local, map[string]string{
		"SHA256SUMS": local["SHA256SUMS"],
	})

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("resume partial draft: %v\n%s", err, output)
	}
	fixture.assertPublished(t, true)
	fixture.assertRemoteMatchesLocal(t)
	fixture.assertUploaded(t, []string{"release-manifest.json"})
	fixture.assertVerifierCallCount(t, 2)
}

func TestPublishReleaseRejectsMismatchedExistingAsset(t *testing.T) {
	fixture := newPublishReleaseFixture(t, true, map[string]string{
		"SHA256SUMS":            "expected bytes\n",
		"release-manifest.json": "{\"schema_version\":1}\n",
	}, map[string]string{
		"SHA256SUMS": "different remote bytes\n",
	})

	output, err := fixture.run(t)
	if err == nil {
		t.Fatalf("mismatched remote asset was accepted:\n%s", output)
	}
	if !strings.Contains(output, "existing draft asset SHA256SUMS differs from the signed bundle") {
		t.Fatalf("mismatched-asset failure was not explicit:\n%s", output)
	}
	fixture.assertPublished(t, false)
	fixture.assertUploaded(t, nil)
	if fixture.callCount(t, "edit") != 0 {
		t.Fatal("mismatched draft was published")
	}
	fixture.assertVerifierCallCount(t, 1)
}

func TestPublishReleaseRejectsAlreadyPublishedRelease(t *testing.T) {
	fixture := newPublishReleaseFixture(t, false, map[string]string{
		"SHA256SUMS": "signed checksums\n",
	}, map[string]string{
		"SHA256SUMS": "signed checksums\n",
	})

	output, err := fixture.run(t)
	if err == nil {
		t.Fatalf("already-published release was accepted:\n%s", output)
	}
	if !strings.Contains(output, "release is no longer the authenticated draft reserved by this workflow") {
		t.Fatalf("published-release failure was not explicit:\n%s", output)
	}
	fixture.assertPublished(t, true)
	fixture.assertUploaded(t, nil)
	if fixture.callCount(t, "edit") != 0 {
		t.Fatal("already-published release was edited")
	}
	fixture.assertVerifierCallCount(t, 1)
}

func newPublishReleaseFixture(t *testing.T, draft bool, local, remote map[string]string) *publishReleaseFixture {
	t.Helper()
	root := t.TempDir()
	releaseDir := filepath.Join(root, "signed release")
	stateDir := filepath.Join(root, "github state")
	toolDir := filepath.Join(root, "fake tools")
	for _, directory := range []string{releaseDir, filepath.Join(stateDir, "remote"), toolDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create fixture directory %s: %v", directory, err)
		}
	}
	for name, content := range local {
		writePublishReleaseFile(t, releaseDir, name, content, 0o644)
	}
	for name, content := range remote {
		writePublishReleaseFile(t, filepath.Join(stateDir, "remote"), name, content, 0o644)
	}

	tag := "v1.2.3"
	commit := strings.Repeat("a", 40)
	repository := "apexload/project"
	marker := "<!-- apexload-release:" + repository + ":" + tag + ":" + commit + " -->\n"
	draftValue := "false\n"
	if draft {
		draftValue = "true\n"
	}
	writePublishReleaseFile(t, stateDir, "draft", draftValue, 0o644)
	writePublishReleaseFile(t, stateDir, "body", marker, 0o644)
	publicKey := writePublishReleaseFile(t, root, "release-signing-key.pem", "test public key\n", 0o644)
	verifier := writePublishReleaseFile(t, toolDir, "verify-release", fakeReleaseVerifier, 0o755)
	writePublishReleaseFile(t, toolDir, "gh", fakeGitHubCLI, 0o755)
	writePublishReleaseFile(t, toolDir, "find", portableFind, 0o755)

	return &publishReleaseFixture{
		releaseDir: releaseDir,
		publicKey:  publicKey,
		verifier:   verifier,
		stateDir:   stateDir,
		toolDir:    toolDir,
		tag:        tag,
		commit:     commit,
		repository: repository,
	}
}

func (fixture *publishReleaseFixture) run(t *testing.T) (string, error) {
	t.Helper()
	command := exec.Command("bash", "release/publish-release.sh",
		fixture.tag,
		fixture.commit,
		fixture.repository,
		fixture.releaseDir,
		fixture.publicKey,
		fixture.verifier,
	)
	command.Env = append(os.Environ(),
		"PATH="+fixture.toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GH_STATE_DIR="+fixture.stateDir,
		"GH_TOKEN=test-token",
		"GITHUB_SERVER_URL=https://github.com",
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func (fixture *publishReleaseFixture) assertPublished(t *testing.T, wantPublished bool) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixture.stateDir, "draft"))
	if err != nil {
		t.Fatalf("read draft state: %v", err)
	}
	gotPublished := strings.TrimSpace(string(content)) == "false"
	if gotPublished != wantPublished {
		t.Errorf("published = %t, want %t", gotPublished, wantPublished)
	}
}

func (fixture *publishReleaseFixture) assertRemoteMatchesLocal(t *testing.T) {
	t.Helper()
	local := readPublishReleaseDirectory(t, fixture.releaseDir)
	remote := readPublishReleaseDirectory(t, filepath.Join(fixture.stateDir, "remote"))
	if !reflect.DeepEqual(remote, local) {
		t.Errorf("remote release assets = %#v, want %#v", remote, local)
	}
}

func (fixture *publishReleaseFixture) assertUploaded(t *testing.T, want []string) {
	t.Helper()
	var got []string
	for _, call := range fixture.calls(t) {
		if len(call) >= 4 && call[0] == "release" && call[1] == "upload" {
			got = append(got, filepath.Base(call[3]))
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uploaded assets = %v, want %v", got, want)
	}
}

func (fixture *publishReleaseFixture) assertVerifierCallCount(t *testing.T, want int) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixture.stateDir, "verifier-calls.tsv"))
	if err != nil {
		t.Fatalf("read verifier calls: %v", err)
	}
	got := len(strings.Split(strings.TrimSpace(string(content)), "\n"))
	if got != want {
		t.Errorf("verifier call count = %d, want %d", got, want)
	}
}

func (fixture *publishReleaseFixture) callCount(t *testing.T, subcommand string) int {
	t.Helper()
	count := 0
	for _, call := range fixture.calls(t) {
		if len(call) >= 2 && call[0] == "release" && call[1] == subcommand {
			count++
		}
	}
	return count
}

func (fixture *publishReleaseFixture) calls(t *testing.T) [][]string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixture.stateDir, "calls.tsv"))
	if err != nil {
		t.Fatalf("read fake gh calls: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	calls := make([][]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			calls = append(calls, strings.Split(line, "\t"))
		}
	}
	return calls
}

func writePublishReleaseFile(t *testing.T, directory, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func readPublishReleaseDirectory(t *testing.T, directory string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory %s: %v", directory, err)
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		result[entry.Name()] = string(content)
	}
	return result
}
