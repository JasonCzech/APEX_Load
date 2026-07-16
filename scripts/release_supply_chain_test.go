package scripts_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeCosignScript = `#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  verify)
    exit 0
    ;;
  verify-attestation)
    image=""
    for argument in "$@"; do
      image="$argument"
    done
    if [[ -n "${FAKE_AMD64_DIGEST:-}" && "$image" == *"@${FAKE_AMD64_DIGEST}" ]]; then
      cat "$FAKE_ATTEST_AMD64"
    elif [[ -n "${FAKE_ARM64_DIGEST:-}" && "$image" == *"@${FAKE_ARM64_DIGEST}" ]]; then
      cat "$FAKE_ATTEST_ARM64"
    elif [[ -n "${FAKE_ATTEST_DEFAULT:-}" ]]; then
      cat "$FAKE_ATTEST_DEFAULT"
    else
      printf 'no fake attestation configured for %s\n' "$image" >&2
      exit 91
    fi
    ;;
  *)
    printf 'unexpected fake cosign command: %s\n' "${1:-}" >&2
    exit 92
    ;;
esac
`

const fakeSkopeoScript = `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != inspect || "${2:-}" != --raw || -z "${3:-}" ]]; then
  printf 'unexpected fake skopeo command\n' >&2
  exit 93
fi
cat "$FAKE_SKOPEO_INDEX"
`

func TestVerifySBOMAttestationBindsExactPredicate(t *testing.T) {
	toolDir := installFakeSupplyChainTools(t, false)
	workingDir := t.TempDir()
	publicKey := writeSupplyChainFile(t, workingDir, "cosign.pub", []byte("test public key\n"))
	sbom := []byte(`{"spdxVersion":"SPDX-2.3","name":"apexload-amd64"}`)
	sbomPath := writeSupplyChainFile(t, workingDir, "image.spdx.json", sbom)
	image := "registry.example.com/apex/apexload-controller@sha256:" + strings.Repeat("a", 64)

	tests := []struct {
		name        string
		attestation []byte
		wantSuccess bool
	}{
		{
			name:        "matching predicate",
			attestation: makeAttestationEnvelope(t, sbom),
			wantSuccess: true,
		},
		{
			name:        "different signed predicate",
			attestation: makeAttestationEnvelope(t, []byte(`{"spdxVersion":"SPDX-2.3","name":"another-image"}`)),
		},
		{
			name:        "malformed cosign output",
			attestation: []byte("not-json\n"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attestationPath := writeSupplyChainFile(t, t.TempDir(), "attestation.json", test.attestation)
			output, err := runSupplyChainCommand(t, "release/verify-sbom-attestation.sh",
				[]string{image, publicKey, sbomPath}, map[string]string{
					"PATH":                toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
					"FAKE_ATTEST_DEFAULT": attestationPath,
				})
			if test.wantSuccess && err != nil {
				t.Fatalf("matching attestation was rejected: %v\n%s", err, output)
			}
			if !test.wantSuccess && err == nil {
				t.Fatalf("invalid attestation was accepted:\n%s", output)
			}
		})
	}
}

func TestSignReleaseSupportsProtectedExternalSigner(t *testing.T) {
	workingDir := t.TempDir()
	releaseDir := filepath.Join(workingDir, "release")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSupplyChainFile(t, releaseDir, "release-manifest.json", []byte(`{"schema_version":1}`))
	writeSupplyChainFile(t, releaseDir, "apexctl-test", []byte("verified release bytes\n"))

	privateKey := filepath.Join(workingDir, "private.pem")
	publicKey := filepath.Join(workingDir, "public.pem")
	if output, err := exec.Command("openssl", "genrsa", "-out", privateKey, "2048").CombinedOutput(); err != nil {
		t.Fatalf("generate signing key: %v\n%s", err, output)
	}
	if output, err := exec.Command("openssl", "rsa", "-in", privateKey, "-pubout", "-out", publicKey).CombinedOutput(); err != nil {
		t.Fatalf("derive verification key: %v\n%s", err, output)
	}

	signer := writeSupplyChainFile(t, workingDir, "kms-signer", []byte(`#!/usr/bin/env bash
set -euo pipefail
openssl dgst -sha256 -sign "$TEST_RELEASE_PRIVATE_KEY" -out "$2" "$1"
`))
	if err := os.Chmod(signer, 0o755); err != nil {
		t.Fatal(err)
	}
	missingPrivateKey := filepath.Join(workingDir, "must-not-be-required.pem")
	output, err := runSupplyChainCommand(t, "release/sign-release.sh",
		[]string{releaseDir, missingPrivateKey, publicKey}, map[string]string{
			"APEXLOAD_RELEASE_SIGNER_PATH": signer,
			"TEST_RELEASE_PRIVATE_KEY":     privateKey,
		})
	if err != nil {
		t.Fatalf("external release signer was rejected: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(releaseDir, "SHA256SUMS.sig")); err != nil {
		t.Fatalf("external signer did not produce the release signature: %v", err)
	}

	badSigner := writeSupplyChainFile(t, workingDir, "bad-signer", []byte(`#!/usr/bin/env bash
set -euo pipefail
printf 'not a signature' > "$2"
`))
	if err := os.Chmod(badSigner, 0o755); err != nil {
		t.Fatal(err)
	}
	output, err = runSupplyChainCommand(t, "release/sign-release.sh",
		[]string{releaseDir, missingPrivateKey, publicKey}, map[string]string{
			"APEXLOAD_RELEASE_SIGNER_PATH": badSigner,
		})
	if err == nil {
		t.Fatalf("invalid external signature was accepted:\n%s", output)
	}
}

func TestVerifyImageEvidenceRejectsWrongSBOMDigestAndIncompletePlatforms(t *testing.T) {
	toolDir := installFakeSupplyChainTools(t, false)
	workingDir := t.TempDir()
	publicKey := writeSupplyChainFile(t, workingDir, "cosign.pub", []byte("test public key\n"))
	amd64SBOM := []byte(`{"spdxVersion":"SPDX-2.3","name":"controller-amd64"}`)
	arm64SBOM := []byte(`{"spdxVersion":"SPDX-2.3","name":"controller-arm64"}`)
	amd64Name := "sbom-apexload-controller-standard-linux-amd64.spdx.json"
	arm64Name := "sbom-apexload-controller-standard-linux-arm64.spdx.json"
	writeSupplyChainFile(t, workingDir, amd64Name, amd64SBOM)
	writeSupplyChainFile(t, workingDir, arm64Name, arm64SBOM)
	amd64Digest := "sha256:" + strings.Repeat("a", 64)
	arm64Digest := "sha256:" + strings.Repeat("b", 64)
	indexDigest := "sha256:" + strings.Repeat("c", 64)
	amd64Attestation := writeSupplyChainFile(t, workingDir, "amd64-attestation.json", makeAttestationEnvelope(t, amd64SBOM))
	arm64Attestation := writeSupplyChainFile(t, workingDir, "arm64-attestation.json", makeAttestationEnvelope(t, arm64SBOM))
	baseEnv := map[string]string{
		"PATH":              toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_AMD64_DIGEST": amd64Digest,
		"FAKE_ARM64_DIGEST": arm64Digest,
		"FAKE_ATTEST_AMD64": amd64Attestation,
		"FAKE_ATTEST_ARM64": arm64Attestation,
	}

	validPlatforms := []imagePlatformEvidence{
		{OS: "linux", Architecture: "amd64", Digest: amd64Digest, SBOMAsset: amd64Name, SBOMSHA256: supplyChainSHA256(amd64SBOM)},
		{OS: "linux", Architecture: "arm64", Digest: arm64Digest, SBOMAsset: arm64Name, SBOMSHA256: supplyChainSHA256(arm64SBOM)},
	}
	validManifest := writeImageEvidenceManifest(t, workingDir, indexDigest, validPlatforms, "valid-manifest.json")
	if output, err := runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, baseEnv); err != nil {
		t.Fatalf("valid image evidence was rejected: %v\n%s", err, output)
	}

	wrongChecksum := append([]imagePlatformEvidence(nil), validPlatforms...)
	wrongChecksum[0].SBOMSHA256 = strings.Repeat("0", 64)
	wrongChecksumManifest := writeImageEvidenceManifest(t, workingDir, indexDigest, wrongChecksum, "wrong-checksum-manifest.json")
	output, err := runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{wrongChecksumManifest, "controller", "standard", publicKey}, baseEnv)
	if err == nil {
		t.Fatalf("manifest with the wrong SBOM checksum was accepted:\n%s", output)
	}
	if !strings.Contains(output, "SBOM asset checksum does not match") {
		t.Fatalf("wrong-checksum failure was not explicit:\n%s", output)
	}

	incompleteManifest := writeImageEvidenceManifest(t, workingDir, indexDigest, validPlatforms[:1], "incomplete-manifest.json")
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{incompleteManifest, "controller", "standard", publicKey}, baseEnv)
	if err == nil {
		t.Fatalf("manifest with missing arm64 evidence was accepted:\n%s", output)
	}
	if !strings.Contains(output, "platform evidence is incomplete") {
		t.Fatalf("missing-platform failure was not explicit:\n%s", output)
	}
}

func TestVerifyRegistryImageEvidenceBindsSBOMsToPlatformDigests(t *testing.T) {
	toolDir := installFakeSupplyChainTools(t, true)
	workingDir := t.TempDir()
	publicKey := writeSupplyChainFile(t, workingDir, "cosign.pub", []byte("test public key\n"))
	amd64SBOM := []byte(`{"spdxVersion":"SPDX-2.3","name":"registry-amd64"}`)
	arm64SBOM := []byte(`{"spdxVersion":"SPDX-2.3","name":"registry-arm64"}`)
	amd64Path := writeSupplyChainFile(t, workingDir, "amd64.spdx.json", amd64SBOM)
	arm64Path := writeSupplyChainFile(t, workingDir, "arm64.spdx.json", arm64SBOM)
	amd64Digest := "sha256:" + strings.Repeat("d", 64)
	arm64Digest := "sha256:" + strings.Repeat("e", 64)
	indexDigest := "sha256:" + strings.Repeat("f", 64)
	indexPath := writeSupplyChainFile(t, workingDir, "index.json", makeRegistryIndex(t,
		registryPlatform{"linux", "amd64", amd64Digest},
		registryPlatform{"linux", "arm64", arm64Digest},
	))
	amd64Attestation := writeSupplyChainFile(t, workingDir, "amd64-attestation.json", makeAttestationEnvelope(t, amd64SBOM))
	arm64Attestation := writeSupplyChainFile(t, workingDir, "arm64-attestation.json", makeAttestationEnvelope(t, arm64SBOM))
	env := map[string]string{
		"PATH":              toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_SKOPEO_INDEX": indexPath,
		"FAKE_AMD64_DIGEST": amd64Digest,
		"FAKE_ARM64_DIGEST": arm64Digest,
		"FAKE_ATTEST_AMD64": amd64Attestation,
		"FAKE_ATTEST_ARM64": arm64Attestation,
	}
	image := "registry.example.com/apex/apexload-controller@" + indexDigest

	if output, err := runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, env); err != nil {
		t.Fatalf("correct platform/SBOM association was rejected: %v\n%s", err, output)
	}

	output, err := runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, arm64Path, amd64Path}, env)
	if err == nil {
		t.Fatalf("swapped platform SBOMs were accepted:\n%s", output)
	}
	if !strings.Contains(output, "attestation predicate does not match") {
		t.Fatalf("swapped-platform failure was not explicit:\n%s", output)
	}
}

func TestVerifyRegistryImageEvidenceRejectsMissingPlatform(t *testing.T) {
	toolDir := installFakeSupplyChainTools(t, true)
	workingDir := t.TempDir()
	publicKey := writeSupplyChainFile(t, workingDir, "cosign.pub", []byte("test public key\n"))
	sbom := []byte(`{"spdxVersion":"SPDX-2.3","name":"registry-image"}`)
	amd64Path := writeSupplyChainFile(t, workingDir, "amd64.spdx.json", sbom)
	arm64Path := writeSupplyChainFile(t, workingDir, "arm64.spdx.json", sbom)
	amd64Digest := "sha256:" + strings.Repeat("1", 64)
	indexPath := writeSupplyChainFile(t, workingDir, "index.json", makeRegistryIndex(t,
		registryPlatform{"linux", "amd64", amd64Digest},
	))
	attestation := writeSupplyChainFile(t, workingDir, "attestation.json", makeAttestationEnvelope(t, sbom))
	env := map[string]string{
		"PATH":              toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_SKOPEO_INDEX": indexPath,
		"FAKE_AMD64_DIGEST": amd64Digest,
		"FAKE_ATTEST_AMD64": attestation,
	}
	image := "registry.example.com/apex/apexload-controller@sha256:" + strings.Repeat("2", 64)

	output, err := runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, env)
	if err == nil {
		t.Fatalf("registry index missing arm64 was accepted:\n%s", output)
	}
	if !strings.Contains(output, "platform evidence is incomplete") {
		t.Fatalf("missing-platform failure was not explicit:\n%s", output)
	}
}

func TestReleaseArtifactNormalizationSupportsMergedAndNestedLayouts(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	normalizeScript := extractWorkflowRunStep(t, string(workflowBytes), "Normalize artifact layout")

	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "GitHub.com merged artifacts",
			files: map[string]string{
				"staging/apexctl_1.2.3_linux_amd64":                                      "apexctl",
				"staging/assets/sbom-apexload-controller-standard-linux-amd64.spdx.json": "sbom",
				"staging/dist/desktop/apexload-desktop-linux-amd64.tar.gz":               "desktop",
				"staging/image-metadata/controller-standard.json":                        "metadata",
			},
		},
		{
			name: "GHES nested artifacts",
			files: map[string]string{
				"staging/release-core-assets/release/assets/apexload-controller_1.2.3_linux_amd64.tar.gz":                          "server",
				"staging/release-client-linux-amd64/release/assets/apexctl_1.2.3_linux_amd64":                                      "apexctl",
				"staging/release-client-linux-amd64/dist/desktop/apexload-desktop-linux-amd64.tar.gz":                              "desktop",
				"staging/release-image-controller-standard/release/assets/sbom-apexload-controller-standard-linux-amd64.spdx.json": "sbom",
				"staging/release-image-controller-standard/release/image-metadata/controller-standard.json":                        "metadata",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workingDir := t.TempDir()
			for name, content := range test.files {
				writeSupplyChainFile(t, workingDir, name, []byte(content))
			}
			toolDir := installPortableMove(t)
			command := exec.Command("bash", "-c", normalizeScript)
			command.Dir = workingDir
			command.Env = append(os.Environ(), "PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("normalize artifact layout: %v\n%s\nscript:\n%s", err, output, normalizeScript)
			}

			for name, content := range test.files {
				base := filepath.Base(name)
				destination := filepath.Join(workingDir, "staging", "assets", base)
				if strings.Contains(name, "/image-metadata/") {
					destination = filepath.Join(workingDir, "staging", "image-metadata", base)
				}
				actual, err := os.ReadFile(destination)
				if err != nil {
					t.Errorf("normalized file %s is missing: %v", destination, err)
					continue
				}
				if string(actual) != content {
					t.Errorf("normalized file %s content = %q, want %q", destination, actual, content)
				}
			}
		})
	}
}

type imagePlatformEvidence struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Digest       string `json:"digest"`
	SBOMAsset    string `json:"sbom_asset"`
	SBOMSHA256   string `json:"sbom_sha256"`
}

type registryPlatform struct {
	OS           string
	Architecture string
	Digest       string
}

func installFakeSupplyChainTools(t *testing.T, includeSkopeo bool) string {
	t.Helper()
	directory := t.TempDir()
	writeSupplyChainFile(t, directory, "cosign", []byte(fakeCosignScript))
	if err := os.Chmod(filepath.Join(directory, "cosign"), 0o755); err != nil {
		t.Fatalf("chmod fake cosign: %v", err)
	}
	if includeSkopeo {
		writeSupplyChainFile(t, directory, "skopeo", []byte(fakeSkopeoScript))
		if err := os.Chmod(filepath.Join(directory, "skopeo"), 0o755); err != nil {
			t.Fatalf("chmod fake skopeo: %v", err)
		}
	}
	return directory
}

func installPortableMove(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	wrapper := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == -t ]]; then
  destination="$2"
  shift 2
  exec /bin/mv "$@" "$destination"
fi
exec /bin/mv "$@"
`
	path := writeSupplyChainFile(t, directory, "mv", []byte(wrapper))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod portable mv: %v", err)
	}
	return directory
}

func runSupplyChainCommand(t *testing.T, script string, arguments []string, environment map[string]string) (string, error) {
	t.Helper()
	command := exec.Command("bash", append([]string{script}, arguments...)...)
	command.Env = os.Environ()
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func writeSupplyChainFile(t *testing.T, directory, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(directory, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func makeAttestationEnvelope(t *testing.T, predicateJSON []byte) []byte {
	t.Helper()
	var predicate any
	if err := json.Unmarshal(predicateJSON, &predicate); err != nil {
		t.Fatalf("decode test predicate: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"_type":     "https://in-toto.io/Statement/v1",
		"predicate": predicate,
	})
	if err != nil {
		t.Fatalf("encode test attestation payload: %v", err)
	}
	envelope, err := json.Marshal([]map[string]string{{
		"payload": base64.StdEncoding.EncodeToString(payload),
	}})
	if err != nil {
		t.Fatalf("encode test attestation envelope: %v", err)
	}
	return envelope
}

func writeImageEvidenceManifest(t *testing.T, directory, indexDigest string, platforms []imagePlatformEvidence, name string) string {
	t.Helper()
	manifest := map[string]any{
		"images": []map[string]any{{
			"component":  "controller",
			"profile":    "standard",
			"repository": "registry.example.com/apex/apexload-controller",
			"digest":     indexDigest,
			"platforms":  platforms,
		}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode image evidence manifest: %v", err)
	}
	return writeSupplyChainFile(t, directory, name, content)
}

func makeRegistryIndex(t *testing.T, platforms ...registryPlatform) []byte {
	t.Helper()
	manifests := make([]map[string]any, 0, len(platforms))
	for _, platform := range platforms {
		manifests = append(manifests, map[string]any{
			"digest": platform.Digest,
			"platform": map[string]string{
				"os":           platform.OS,
				"architecture": platform.Architecture,
			},
		})
	}
	content, err := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": manifests})
	if err != nil {
		t.Fatalf("encode registry index: %v", err)
	}
	return content
}

func supplyChainSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func extractWorkflowRunStep(t *testing.T, workflow, stepName string) string {
	t.Helper()
	marker := "      - name: " + stepName + "\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		t.Fatalf("workflow step %q not found", stepName)
	}
	remainder := workflow[start+len(marker):]
	runMarker := "        run: |\n"
	if !strings.HasPrefix(remainder, runMarker) {
		t.Fatalf("workflow step %q does not begin with a literal run block", stepName)
	}
	remainder = remainder[len(runMarker):]
	var script strings.Builder
	for _, line := range strings.Split(remainder, "\n") {
		if strings.HasPrefix(line, "          ") {
			script.WriteString(strings.TrimPrefix(line, "          "))
			script.WriteByte('\n')
			continue
		}
		if line == "" {
			script.WriteByte('\n')
			continue
		}
		break
	}
	if strings.TrimSpace(script.String()) == "" {
		t.Fatalf("workflow step %q has an empty run block", stepName)
	}
	return script.String()
}
