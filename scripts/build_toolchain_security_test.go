package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	reviewedBuildxVersion  = "v0.34.1"
	reviewedBuildkitDigest = "sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a"
	reviewedBuildkitImage  = "docker.io/moby/buildkit@" + reviewedBuildkitDigest
	reviewedNSISVersion    = "3.10"
	reviewedNSISSHA256     = "fcdce3229717a2a148e7cda0ab5bdb667f39d8fb33ede1da8dabc336bd5ad110"
)

func TestReleaseWindowsBuilderUsesChecksumPinnedNSIS(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	installerBytes, err := os.ReadFile("ci/install-nsis.ps1")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	installer := string(installerBytes)
	if err := validateReleaseNSISPin(workflow, installer); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		workflow  string
		installer string
	}{
		{
			name:     "mutated archive checksum",
			workflow: workflow,
			installer: strings.Replace(installer,
				reviewedNSISSHA256,
				strings.Repeat("0", 64), 1),
		},
		{
			name:      "missing archive checksum verification",
			workflow:  workflow,
			installer: strings.Replace(installer, "Get-FileHash -LiteralPath $archivePath -Algorithm SHA256", "Write-Output $archivePath", 1),
		},
		{
			name:      "mutable NSIS version",
			workflow:  workflow,
			installer: strings.Replace(installer, `$nsisVersion = "3.10"`, `$nsisVersion = "latest"`, 1),
		},
		{
			name:      "unverified package manager restored",
			workflow:  strings.Replace(workflow, "& ./scripts/ci/install-nsis.ps1", "choco install nsis --version=3.10", 1),
			installer: installer,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateReleaseNSISPin(test.workflow, test.installer); err == nil {
				t.Fatal("mutated NSIS trust policy was accepted")
			}
		})
	}
}

func validateReleaseNSISPin(workflow, installer string) error {
	for _, required := range []string{
		"- name: Install checksum-pinned NSIS",
		"APEXLOAD_USE_PREINSTALLED_TOOLS: ${{ vars.APEXLOAD_USE_PREINSTALLED_TOOLS }}",
		"NSIS_RELEASE_BASE_URL: ${{ vars.NSIS_RELEASE_BASE_URL }}",
		"NSIS_ARCHIVE_PATH: ${{ vars.NSIS_ARCHIVE_PATH }}",
		"& ./scripts/ci/install-nsis.ps1",
	} {
		if !strings.Contains(workflow, required) {
			return fmt.Errorf("release workflow is missing NSIS trust contract %q", required)
		}
	}
	if strings.Contains(workflow, "choco install nsis") {
		return fmt.Errorf("protected release installs NSIS through an unauthenticated package-manager path")
	}
	for _, required := range []string{
		`$nsisVersion = "` + reviewedNSISVersion + `"`,
		`$nsisArchiveSha256 = "` + reviewedNSISSHA256 + `"`,
		"Get-FileHash -LiteralPath $archivePath -Algorithm SHA256",
		"if ($actualSha256 -ne $nsisArchiveSha256)",
		"https://downloads.sourceforge.net/project/nsis/NSIS%203/$nsisVersion",
		"NSIS_ARCHIVE_PATH must point to the reviewed NSIS archive",
		"Expand-Archive -LiteralPath $archivePath",
		`if ($reportedVersion -ne "v$nsisVersion")`,
	} {
		if !strings.Contains(installer, required) {
			return fmt.Errorf("NSIS installer is missing %q", required)
		}
	}
	return nil
}

func TestReleaseBuildersPinAndVerifyBuildxAndBuildKit(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	installerBytes, err := os.ReadFile("ci/install-buildx.sh")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	installer := string(installerBytes)
	if err := validateReleaseBuilderPins(workflow, installer); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		workflow  string
		installer string
	}{
		{
			name:      "missing Buildx version",
			workflow:  workflow,
			installer: strings.Replace(installer, `readonly BUILDX_VERSION="v0.34.1"`, `readonly BUILDX_VERSION=""`, 1),
		},
		{
			name:      "mutable Buildx version",
			workflow:  workflow,
			installer: strings.Replace(installer, `readonly BUILDX_VERSION="v0.34.1"`, `readonly BUILDX_VERSION="latest"`, 1),
		},
		{
			name:     "missing Buildx checksum",
			workflow: workflow,
			installer: strings.Replace(installer,
				`readonly BUILDX_LINUX_AMD64_SHA256="f1332ddb9010bd0b72628266c3a906d9a6979848033df4c8d9bd2cd113bae12b"`,
				`readonly BUILDX_LINUX_AMD64_SHA256=""`, 1),
		},
		{
			name: "mutable BuildKit image",
			workflow: strings.Replace(workflow,
				"BUILDKIT_IMAGE: ${{ vars.APEXLOAD_BUILDKIT_IMAGE || '"+reviewedBuildkitImage+"' }}",
				"BUILDKIT_IMAGE: moby/buildkit:buildx-stable-1", 1),
			installer: installer,
		},
		{
			name: "missing runtime verification",
			workflow: strings.Replace(workflow,
				`run: ./scripts/ci/verify-buildx-toolchain.sh builder "$BUILDER_NAME" "$BUILDKIT_IMAGE"`,
				"run: docker buildx version", 1),
			installer: installer,
		},
		{
			name:      "unverified setup action restored",
			workflow:  workflow + "\nuses: docker/setup-buildx-action@e468171a9de216ec08956ac3ada2f0791b6bd435\n",
			installer: installer,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateReleaseBuilderPins(test.workflow, test.installer); err == nil {
				t.Fatal("mutated release builder policy was accepted")
			}
		})
	}
}

func validateReleaseBuilderPins(workflow, installer string) error {
	if strings.Contains(workflow, "docker/setup-buildx-action@") {
		return fmt.Errorf("release workflow still delegates Buildx download to setup-buildx-action")
	}
	if !strings.Contains(workflow, `platforms: ${{ runner.arch == 'ARM64' && 'amd64' || 'arm64' }}`) {
		return fmt.Errorf("release image build does not install the non-native QEMU platform on both X64 and ARM64 runners")
	}
	if strings.Contains(workflow, "platforms: arm64\n") {
		return fmt.Errorf("release image build still assumes every release runner is amd64")
	}
	for fragment, want := range map[string]int{
		`- name: Install checksum-pinned registry inspection`:          1,
		`SKOPEO_RELEASE_BASE_URL: ${{ vars.SKOPEO_RELEASE_BASE_URL }}`: 1,
		`./scripts/ci/install-skopeo.sh "$tool_directory"`:             1,
		`[[ "$(skopeo --version)" == 'skopeo version 1.20.0' ]]`:       1,
		`APEXLOAD_IMAGE_INSPECTOR: skopeo`:                             1,
	} {
		if got := strings.Count(workflow, fragment); got != want {
			return fmt.Errorf("release workflow count for %q = %d, want %d", fragment, got, want)
		}
	}

	for fragment, want := range map[string]int{
		`- name: Install checksum-pinned Buildx`:                                                 2,
		`BUILDX_RELEASE_BASE_URL: ${{ vars.BUILDX_RELEASE_BASE_URL }}`:                           2,
		`./scripts/ci/install-buildx.sh "$docker_config"`:                                        2,
		`printf 'DOCKER_CONFIG=%s\n' "$docker_config" >> "$GITHUB_ENV"`:                          2,
		`run: ./scripts/ci/verify-buildx-toolchain.sh image "$BUILDKIT_IMAGE"`:                   2,
		`./scripts/ci/verify-buildx-toolchain.sh binary`:                                         2,
		`run: ./scripts/ci/verify-buildx-toolchain.sh builder "$BUILDER_NAME" "$BUILDKIT_IMAGE"`: 2,
		`docker buildx create \`:                                                                 2,
		`--driver-opt "image=$BUILDKIT_IMAGE"`:                                                   2,
	} {
		if got := strings.Count(workflow, fragment); got != want {
			return fmt.Errorf("release workflow count for %q = %d, want %d", fragment, got, want)
		}
	}
	if got := strings.Count(workflow,
		"BUILDKIT_IMAGE: ${{ vars.APEXLOAD_BUILDKIT_IMAGE || '"+reviewedBuildkitImage+"' }}"); got != 6 {
		return fmt.Errorf("release workflow reviewed BuildKit references = %d, want 6", got)
	}
	for _, required := range []string{
		"- name: Remove release builder and isolated registry credentials\n        if: always()",
		"- name: Remove manifest builder and isolated registry credentials\n        if: always()",
	} {
		if !strings.Contains(workflow, required) {
			return fmt.Errorf("release workflow is missing cleanup contract %q", required)
		}
	}
	for _, required := range []string{
		`readonly BUILDX_VERSION="` + reviewedBuildxVersion + `"`,
		`readonly BUILDX_LINUX_AMD64_SHA256="f1332ddb9010bd0b72628266c3a906d9a6979848033df4c8d9bd2cd113bae12b"`,
		`readonly BUILDX_LINUX_ARM64_SHA256="c34e32dd6ea2653d960d6c099c9f09b9077e4a37504d2d31e5066eccc3904231"`,
		`https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}`,
		`--proto-redir '=https'`,
		`sha256sum --check --status`,
	} {
		if !strings.Contains(installer, required) {
			return fmt.Errorf("Buildx installer is missing %q", required)
		}
	}
	if strings.Contains(workflow, "moby/buildkit:buildx-stable") ||
		strings.Contains(installer, `BUILDX_VERSION="latest"`) {
		return fmt.Errorf("release workflow contains a mutable Buildx or BuildKit reference")
	}
	return nil
}

func TestAKSUsesChecksumPinnedSkopeoWithoutBuildKit(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/deploy-aks.yml")
	if err != nil {
		t.Fatal(err)
	}
	installerBytes, err := os.ReadFile("ci/install-skopeo.sh")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	installer := string(installerBytes)
	if err := validateAKSInspectionTool(workflow, installer); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		workflow  string
		installer string
	}{
		{
			name:      "missing installer",
			workflow:  strings.Replace(workflow, `./scripts/ci/install-skopeo.sh "$tool_directory"`, "true", 1),
			installer: installer,
		},
		{
			name:      "mutable installer version",
			workflow:  workflow,
			installer: strings.Replace(installer, `readonly SKOPEO_VERSION="1.20.0"`, `readonly SKOPEO_VERSION="latest"`, 1),
		},
		{
			name:     "missing amd64 checksum",
			workflow: workflow,
			installer: strings.Replace(installer,
				`readonly SKOPEO_LINUX_AMD64_SHA256="e5d3f410839313c0b28ef4659643146dad88653fc831aa15adf705421fa06fc0"`,
				`readonly SKOPEO_LINUX_AMD64_SHA256=""`, 1),
		},
		{
			name:      "BuildKit started in deployment",
			workflow:  workflow + "\nrun: docker buildx create --use\n",
			installer: installer,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAKSInspectionTool(test.workflow, test.installer); err == nil {
				t.Fatal("mutated AKS inspection-tool policy was accepted")
			}
		})
	}
}

func validateAKSInspectionTool(workflow, installer string) error {
	for _, prohibited := range []string{"docker/setup-buildx-action@", "docker buildx"} {
		if strings.Contains(workflow, prohibited) {
			return fmt.Errorf("AKS deployment starts or installs Buildx via %q", prohibited)
		}
	}
	for _, required := range []string{
		"Install checksum-pinned registry inspection",
		"SKOPEO_RELEASE_BASE_URL: ${{ vars.SKOPEO_RELEASE_BASE_URL }}",
		`./scripts/ci/install-skopeo.sh "$tool_directory"`,
		`[[ "$(skopeo --version)" == 'skopeo version 1.20.0' ]]`,
	} {
		if !strings.Contains(workflow, required) {
			return fmt.Errorf("AKS workflow is missing %q", required)
		}
	}
	for _, required := range []string{
		`readonly SKOPEO_VERSION="1.20.0"`,
		`readonly SKOPEO_LINUX_AMD64_SHA256="e5d3f410839313c0b28ef4659643146dad88653fc831aa15adf705421fa06fc0"`,
		`readonly SKOPEO_LINUX_ARM64_SHA256="c53670037b0a861ffaae1ce11baaca511d0722e4b537ab11c19a04660d8e6021"`,
		`https://github.com/lework/skopeo-binary/releases/download/v${SKOPEO_VERSION}`,
		`--proto-redir '=https'`,
		`sha256sum --check --status`,
	} {
		if !strings.Contains(installer, required) {
			return fmt.Errorf("Skopeo installer is missing %q", required)
		}
	}
	if strings.Contains(installer, `SKOPEO_VERSION="latest"`) {
		return fmt.Errorf("Skopeo installer uses a mutable version")
	}
	return nil
}

func TestBuildxToolchainVerifierRejectsMutableOrMismatchedTools(t *testing.T) {
	toolDirectory := t.TempDir()
	fakeDocker := `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == buildx && "$2" == version ]]; then
  printf '%s\n' "${FAKE_BUILDX_VERSION:-github.com/docker/buildx v0.34.1 deadbeef}"
elif [[ "$1" == buildx && "$2" == inspect ]]; then
  cat <<EOF
Name:          $3
Driver:        docker-container
Nodes:
Name:          ${3}0
${FAKE_BUILDKIT_LABEL:-BuildKit version:} ${FAKE_BUILDKIT_VERSION:-v0.31.1}
EOF
elif [[ "$1" == inspect && "$2" == --format && "$3" == '{{.Config.Image}}' ]]; then
  printf '%s\n' "${FAKE_BUILDKIT_IMAGE:-docker.io/moby/buildkit@sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a}"
elif [[ "$1" == inspect && "$2" == --format && "$3" == '{{.Image}}' ]]; then
  printf '%s\n' "${FAKE_CONTAINER_IMAGE_ID:-sha256:reviewed-image-id}"
elif [[ "$1" == image && "$2" == inspect && "$3" == --format && "$4" == '{{.Id}}' ]]; then
  printf '%s\n' "${FAKE_REVIEWED_IMAGE_ID:-sha256:reviewed-image-id}"
else
  printf 'unexpected fake docker command: %q\n' "$*" >&2
  exit 97
fi
`
	writeExecutable(t, filepath.Join(toolDirectory, "docker"), fakeDocker)

	run := func(args []string, extraEnv ...string) ([]byte, error) {
		command := exec.Command("bash", append([]string{"ci/verify-buildx-toolchain.sh"}, args...)...)
		command.Env = append(os.Environ(),
			"PATH="+toolDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		command.Env = append(command.Env, extraEnv...)
		return command.CombinedOutput()
	}

	if output, err := run([]string{"builder", "apexload-release-1", reviewedBuildkitImage}); err != nil {
		t.Fatalf("reviewed toolchain was rejected: %v\n%s", err, output)
	}
	for _, test := range []struct {
		name string
		args []string
		env  []string
	}{
		{name: "mutable image", args: []string{"image", "moby/buildkit:v0.31.1"}},
		{name: "wrong image digest", args: []string{"image", "docker.io/moby/buildkit@sha256:" + strings.Repeat("1", 64)}},
		{name: "wrong Buildx version", args: []string{"binary"}, env: []string{"FAKE_BUILDX_VERSION=github.com/docker/buildx v0.34.2 deadbeef"}},
		{name: "wrong BuildKit version", args: []string{"builder", "apexload-release-1", reviewedBuildkitImage}, env: []string{"FAKE_BUILDKIT_VERSION=v0.31.2"}},
		{name: "malformed BuildKit output", args: []string{"builder", "apexload-release-1", reviewedBuildkitImage}, env: []string{"FAKE_BUILDKIT_LABEL=BuildKit:"}},
		{name: "builder ignored digest", args: []string{"builder", "apexload-release-1", reviewedBuildkitImage}, env: []string{"FAKE_BUILDKIT_IMAGE=moby/buildkit:buildx-stable-1"}},
		{name: "container image ID mismatch", args: []string{"builder", "apexload-release-1", reviewedBuildkitImage}, env: []string{"FAKE_CONTAINER_IMAGE_ID=sha256:other-image-id"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if output, err := run(test.args, test.env...); err == nil {
				t.Fatalf("unreviewed toolchain was accepted:\n%s", output)
			}
		})
	}
}

func TestBuildxInstallerRejectsUntrustedDownloads(t *testing.T) {
	toolDirectory := t.TempDir()
	fakeUname := `#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exit 2 ;;
esac
`
	fakeCurl := `#!/usr/bin/env bash
set -euo pipefail
destination=
while (($#)); do
  if [[ "$1" == --output ]]; then
    destination="$2"
    shift 2
  else
    shift
  fi
done
[[ -n "$destination" ]]
printf 'tampered buildx binary\n' > "$destination"
`
	writeExecutable(t, filepath.Join(toolDirectory, "uname"), fakeUname)
	writeExecutable(t, filepath.Join(toolDirectory, "curl"), fakeCurl)

	command := exec.Command("bash", "ci/install-buildx.sh", filepath.Join(t.TempDir(), "docker-config"))
	command.Env = append(os.Environ(),
		"PATH="+toolDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BUILDX_RELEASE_BASE_URL=https://mirror.example.invalid/buildx/v0.34.1",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("tampered Buildx download was accepted:\n%s", output)
	}
	if !strings.Contains(string(output), "checksum verification failed") {
		t.Fatalf("Buildx checksum failure was not explicit:\n%s", output)
	}

	command = exec.Command("bash", "ci/install-buildx.sh", filepath.Join(t.TempDir(), "docker-config"))
	command.Env = append(os.Environ(),
		"PATH="+toolDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BUILDX_RELEASE_BASE_URL=http://mirror.example.invalid/buildx/v0.34.1",
	)
	output, err = command.CombinedOutput()
	if err == nil {
		t.Fatalf("insecure Buildx mirror URL was accepted:\n%s", output)
	}
	if !strings.Contains(string(output), "must be an HTTPS URL") {
		t.Fatalf("insecure Buildx mirror failure was not explicit:\n%s", output)
	}
}

func TestSkopeoInstallerRejectsUntrustedDownloads(t *testing.T) {
	toolDirectory := t.TempDir()
	fakeUname := `#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exit 2 ;;
esac
`
	fakeCurl := `#!/usr/bin/env bash
set -euo pipefail
destination=
while (($#)); do
  if [[ "$1" == --output ]]; then
    destination="$2"
    shift 2
  else
    shift
  fi
done
[[ -n "$destination" ]]
printf 'tampered skopeo binary\n' > "$destination"
`
	writeExecutable(t, filepath.Join(toolDirectory, "uname"), fakeUname)
	writeExecutable(t, filepath.Join(toolDirectory, "curl"), fakeCurl)

	command := exec.Command("bash", "ci/install-skopeo.sh", filepath.Join(t.TempDir(), "bin"))
	command.Env = append(os.Environ(),
		"PATH="+toolDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SKOPEO_RELEASE_BASE_URL=https://mirror.example.invalid/skopeo/v1.20.0",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("tampered Skopeo download was accepted:\n%s", output)
	}
	if !strings.Contains(string(output), "checksum verification failed") {
		t.Fatalf("checksum failure was not explicit:\n%s", output)
	}

	command = exec.Command("bash", "ci/install-skopeo.sh", filepath.Join(t.TempDir(), "bin"))
	command.Env = append(os.Environ(),
		"PATH="+toolDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SKOPEO_RELEASE_BASE_URL=http://mirror.example.invalid/skopeo/v1.20.0",
	)
	output, err = command.CombinedOutput()
	if err == nil {
		t.Fatalf("insecure Skopeo mirror URL was accepted:\n%s", output)
	}
	if !strings.Contains(string(output), "must be an HTTPS URL") {
		t.Fatalf("insecure-mirror failure was not explicit:\n%s", output)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
