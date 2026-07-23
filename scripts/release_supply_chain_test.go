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

const (
	fakeAMD64Manifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":64},"layers":[]}`
	fakeARM64Manifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","size":64},"layers":[]}`
	fakeAMD64Config   = `{"architecture":"amd64","os":"linux"}`
	fakeARM64Config   = `{"architecture":"arm64","os":"linux"}`
)

const fakeSkopeoScript = `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != inspect || -z "${2:-}" || -z "${3:-}" ]]; then
  printf 'unexpected fake skopeo command\n' >&2
  exit 93
fi
mode="$2"
reference="$3"
if [[ "$mode" == --config ]]; then
  if [[ -n "${FAKE_AMD64_DIGEST:-}" && "$reference" == *"@${FAKE_AMD64_DIGEST}" ]]; then
    if [[ -n "${FAKE_AMD64_CONFIG:-}" ]]; then
      cat "$FAKE_AMD64_CONFIG"
    else
      printf '%s\n' '{"architecture":"amd64","os":"linux"}'
    fi
    exit 0
  fi
  if [[ -n "${FAKE_ARM64_DIGEST:-}" && "$reference" == *"@${FAKE_ARM64_DIGEST}" ]]; then
    if [[ -n "${FAKE_ARM64_CONFIG:-}" ]]; then
      cat "$FAKE_ARM64_CONFIG"
    else
      printf '%s\n' '{"architecture":"arm64","os":"linux"}'
    fi
    exit 0
  fi
  printf 'unexpected fake skopeo config reference\n' >&2
  exit 94
fi
if [[ "$mode" != --raw ]]; then
  printf 'unexpected fake skopeo inspection mode\n' >&2
  exit 95
fi
if [[ -n "${FAKE_AMD64_DIGEST:-}" && "$reference" == *"@${FAKE_AMD64_DIGEST}" ]]; then
  if [[ -n "${FAKE_AMD64_MANIFEST:-}" ]]; then
    cat "$FAKE_AMD64_MANIFEST"
  else
    printf '%s\n' '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":64},"layers":[]}'
  fi
  exit 0
fi
if [[ -n "${FAKE_ARM64_DIGEST:-}" && "$reference" == *"@${FAKE_ARM64_DIGEST}" ]]; then
  if [[ -n "${FAKE_ARM64_MANIFEST:-}" ]]; then
    cat "$FAKE_ARM64_MANIFEST"
  else
    printf '%s\n' '{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","size":64},"layers":[]}'
  fi
  exit 0
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

func TestVerifyReleaseRejectsUnsignedExtraAsset(t *testing.T) {
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
	if output, err := runSupplyChainCommand(t, "release/sign-release.sh",
		[]string{releaseDir, privateKey, publicKey}, nil); err != nil {
		t.Fatalf("sign release fixture: %v\n%s", err, output)
	}
	writeSupplyChainFile(t, releaseDir, "unsigned-extra.bin", []byte("must not be ignored\n"))
	output, err := runSupplyChainCommand(t, "release/verify-release.sh",
		[]string{releaseDir, "v1.2.3", publicKey}, nil)
	if err == nil {
		t.Fatalf("release verifier accepted an unsigned extra asset:\n%s", output)
	}
	if !strings.Contains(output, "asset set does not exactly match signed SHA256SUMS") {
		t.Fatalf("unsigned-extra failure was not explicit:\n%s", output)
	}
}

func TestVerifyImageEvidenceRejectsWrongSBOMDigestAndIncompletePlatforms(t *testing.T) {
	toolDir := installFakeSupplyChainTools(t, true)
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
	indexPath := writeSupplyChainFile(t, workingDir, "image-index.json", makeRegistryIndex(t,
		registryPlatform{OS: "linux", Architecture: "amd64", Digest: amd64Digest},
		registryPlatform{
			OS: "linux", Architecture: "arm64", Digest: arm64Digest,
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		},
	))
	amd64Attestation := writeSupplyChainFile(t, workingDir, "amd64-attestation.json", makeAttestationEnvelope(t, amd64SBOM))
	arm64Attestation := writeSupplyChainFile(t, workingDir, "arm64-attestation.json", makeAttestationEnvelope(t, arm64SBOM))
	baseEnv := map[string]string{
		"APEXLOAD_IMAGE_INSPECTOR": "skopeo",
		"PATH":                     toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_SKOPEO_INDEX":        indexPath,
		"FAKE_AMD64_DIGEST":        amd64Digest,
		"FAKE_ARM64_DIGEST":        arm64Digest,
		"FAKE_ATTEST_AMD64":        amd64Attestation,
		"FAKE_ATTEST_ARM64":        arm64Attestation,
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

	implicitInspectorEnv := make(map[string]string, len(baseEnv))
	for key, value := range baseEnv {
		implicitInspectorEnv[key] = value
	}
	implicitInspectorEnv["APEXLOAD_IMAGE_INSPECTOR"] = "auto"
	output, err := runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, implicitInspectorEnv)
	if err == nil {
		t.Fatalf("implicit image inspector selection was accepted:\n%s", output)
	}
	if !strings.Contains(output, "must explicitly select skopeo") {
		t.Fatalf("implicit-inspector failure was not explicit:\n%s", output)
	}

	wrongChecksum := append([]imagePlatformEvidence(nil), validPlatforms...)
	wrongChecksum[0].SBOMSHA256 = strings.Repeat("0", 64)
	wrongChecksumManifest := writeImageEvidenceManifest(t, workingDir, indexDigest, wrongChecksum, "wrong-checksum-manifest.json")
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
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

	duplicateDigest := append([]imagePlatformEvidence(nil), validPlatforms...)
	duplicateDigest[1].Digest = duplicateDigest[0].Digest
	duplicateDigestManifest := writeImageEvidenceManifest(t, workingDir, indexDigest, duplicateDigest, "duplicate-digest-manifest.json")
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{duplicateDigestManifest, "controller", "standard", publicKey}, baseEnv)
	if err == nil {
		t.Fatalf("two release platforms sharing one image digest were accepted:\n%s", output)
	}
	if !strings.Contains(output, "platform evidence is incomplete or duplicated") {
		t.Fatalf("duplicate-platform-digest failure was not explicit:\n%s", output)
	}

	wrongSizeIndex := writeSupplyChainFile(t, workingDir, "wrong-size-image-index.json", makeRegistryIndex(t,
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: amd64Digest,
			Size: len(fakeAMD64Manifest) + 2,
		},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
	))
	wrongSizeEnv := make(map[string]string, len(baseEnv))
	for key, value := range baseEnv {
		wrongSizeEnv[key] = value
	}
	wrongSizeEnv["FAKE_SKOPEO_INDEX"] = wrongSizeIndex
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, wrongSizeEnv)
	if err == nil {
		t.Fatalf("platform descriptor with the wrong byte size was accepted:\n%s", output)
	}
	if !strings.Contains(output, "descriptor size does not match") {
		t.Fatalf("wrong descriptor-size failure was not explicit:\n%s", output)
	}

	mislabeledConfig := writeSupplyChainFile(t, workingDir, "mislabeled-arm64-config.json", []byte(fakeAMD64Config))
	mislabeledConfigEnv := make(map[string]string, len(baseEnv)+1)
	for key, value := range baseEnv {
		mislabeledConfigEnv[key] = value
	}
	mislabeledConfigEnv["FAKE_ARM64_CONFIG"] = mislabeledConfig
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, mislabeledConfigEnv)
	if err == nil {
		t.Fatalf("arm64 descriptor pointing to an amd64 image configuration was accepted:\n%s", output)
	}
	if !strings.Contains(output, "configuration does not match linux/arm64") {
		t.Fatalf("mislabeled-config failure was not explicit:\n%s", output)
	}

	mismatchedIndex := writeSupplyChainFile(t, workingDir, "mismatched-image-index.json", makeRegistryIndex(t,
		registryPlatform{OS: "linux", Architecture: "amd64", Digest: "sha256:" + strings.Repeat("d", 64)},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
	))
	mismatchEnv := make(map[string]string, len(baseEnv))
	for key, value := range baseEnv {
		mismatchEnv[key] = value
	}
	mismatchEnv["FAKE_SKOPEO_INDEX"] = mismatchedIndex
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, mismatchEnv)
	if err == nil {
		t.Fatalf("manifest whose platform digests are not members of the signed index was accepted:\n%s", output)
	}
	if !strings.Contains(output, "direct platform manifests matching") {
		t.Fatalf("index-membership failure was not explicit:\n%s", output)
	}

	nestedIndex := writeSupplyChainFile(t, workingDir, "nested-image-index.json", makeRegistryIndex(t,
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: amd64Digest,
			MediaType: "application/vnd.oci.image.index.v1+json",
		},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
	))
	nestedEnv := make(map[string]string, len(baseEnv))
	for key, value := range baseEnv {
		nestedEnv[key] = value
	}
	nestedEnv["FAKE_SKOPEO_INDEX"] = nestedIndex
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, nestedEnv)
	if err == nil {
		t.Fatalf("nested platform index was accepted as runnable image evidence:\n%s", output)
	}
	if !strings.Contains(output, "direct platform manifests matching") {
		t.Fatalf("nested-index failure was not explicit:\n%s", output)
	}

	nestedChild := writeSupplyChainFile(t, workingDir, "mislabeled-nested-child.json", makeRegistryIndex(t,
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: "sha256:" + strings.Repeat("e", 64),
		},
	))
	mislabeledChildEnv := make(map[string]string, len(baseEnv)+1)
	for key, value := range baseEnv {
		mislabeledChildEnv[key] = value
	}
	mislabeledChildEnv["FAKE_AMD64_MANIFEST"] = nestedChild
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, mislabeledChildEnv)
	if err == nil {
		t.Fatalf("descriptor-labeled nested child was accepted as a runnable image manifest:\n%s", output)
	}
	if !strings.Contains(output, "not a direct runnable image manifest") {
		t.Fatalf("mislabeled nested-child failure was not explicit:\n%s", output)
	}

	wrongChildMediaType := writeSupplyChainFile(t, workingDir, "wrong-child-media-type.json", []byte(
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","config":{"digest":"sha256:`+
			strings.Repeat("1", 64)+`"},"layers":[]}`))
	wrongChildMediaTypeEnv := make(map[string]string, len(baseEnv)+1)
	for key, value := range baseEnv {
		wrongChildMediaTypeEnv[key] = value
	}
	wrongChildMediaTypeEnv["FAKE_AMD64_MANIFEST"] = wrongChildMediaType
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, wrongChildMediaTypeEnv)
	if err == nil {
		t.Fatalf("child with a non-image media type was accepted as runnable image evidence:\n%s", output)
	}
	if !strings.Contains(output, "not a direct runnable image manifest") {
		t.Fatalf("wrong child media-type failure was not explicit:\n%s", output)
	}

	artifactConfig := writeSupplyChainFile(t, workingDir, "artifact-config-child.json", []byte(
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:`+
			strings.Repeat("3", 64)+`"},"layers":[]}`))
	artifactConfigEnv := make(map[string]string, len(baseEnv)+1)
	for key, value := range baseEnv {
		artifactConfigEnv[key] = value
	}
	artifactConfigEnv["FAKE_AMD64_MANIFEST"] = artifactConfig
	output, err = runSupplyChainCommand(t, "release/verify-image-evidence.sh",
		[]string{validManifest, "controller", "standard", publicKey}, artifactConfigEnv)
	if err == nil {
		t.Fatalf("OCI artifact config was accepted as a runnable image:\n%s", output)
	}
	if !strings.Contains(output, "not a direct runnable image manifest") {
		t.Fatalf("artifact-config failure was not explicit:\n%s", output)
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
		registryPlatform{OS: "linux", Architecture: "amd64", Digest: amd64Digest},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
	))
	amd64Attestation := writeSupplyChainFile(t, workingDir, "amd64-attestation.json", makeAttestationEnvelope(t, amd64SBOM))
	arm64Attestation := writeSupplyChainFile(t, workingDir, "arm64-attestation.json", makeAttestationEnvelope(t, arm64SBOM))
	env := map[string]string{
		"APEXLOAD_IMAGE_INSPECTOR": "skopeo",
		"PATH":                     toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_SKOPEO_INDEX":        indexPath,
		"FAKE_AMD64_DIGEST":        amd64Digest,
		"FAKE_ARM64_DIGEST":        arm64Digest,
		"FAKE_ATTEST_AMD64":        amd64Attestation,
		"FAKE_ATTEST_ARM64":        arm64Attestation,
	}
	image := "registry.example.com/apex/apexload-controller@" + indexDigest

	if output, err := runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, env); err != nil {
		t.Fatalf("correct platform/SBOM association was rejected: %v\n%s", err, output)
	}

	mislabeledConfig := writeSupplyChainFile(t, workingDir, "registry-mislabeled-arm64-config.json", []byte(fakeAMD64Config))
	mislabeledConfigEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		mislabeledConfigEnv[key] = value
	}
	mislabeledConfigEnv["FAKE_ARM64_CONFIG"] = mislabeledConfig
	output, err := runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, mislabeledConfigEnv)
	if err == nil {
		t.Fatalf("registry verifier accepted arm64 evidence backed by an amd64 configuration:\n%s", output)
	}
	if !strings.Contains(output, "configuration does not match linux/arm64") {
		t.Fatalf("registry mislabeled-config failure was not explicit:\n%s", output)
	}

	wrongSizeIndex := writeSupplyChainFile(t, workingDir, "registry-wrong-size-index.json", makeRegistryIndex(t,
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: amd64Digest,
			Size: len(fakeAMD64Manifest) + 2,
		},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
	))
	wrongSizeEnv := make(map[string]string, len(env))
	for key, value := range env {
		wrongSizeEnv[key] = value
	}
	wrongSizeEnv["FAKE_SKOPEO_INDEX"] = wrongSizeIndex
	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, wrongSizeEnv)
	if err == nil {
		t.Fatalf("registry verifier accepted a platform descriptor with the wrong byte size:\n%s", output)
	}
	if !strings.Contains(output, "descriptor size does not match") {
		t.Fatalf("registry wrong-size failure was not explicit:\n%s", output)
	}

	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, arm64Path, amd64Path}, env)
	if err == nil {
		t.Fatalf("swapped platform SBOMs were accepted:\n%s", output)
	}
	if !strings.Contains(output, "attestation predicate does not match") {
		t.Fatalf("swapped-platform failure was not explicit:\n%s", output)
	}

	nestedIndex := writeSupplyChainFile(t, workingDir, "nested-index.json", makeRegistryIndex(t,
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: amd64Digest,
			MediaType: "application/vnd.docker.distribution.manifest.list.v2+json",
		},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
	))
	nestedEnv := make(map[string]string, len(env))
	for key, value := range env {
		nestedEnv[key] = value
	}
	nestedEnv["FAKE_SKOPEO_INDEX"] = nestedIndex
	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, nestedEnv)
	if err == nil {
		t.Fatalf("registry verifier accepted a nested platform index:\n%s", output)
	}
	if !strings.Contains(output, "platform evidence is incomplete") {
		t.Fatalf("nested registry-index failure was not explicit:\n%s", output)
	}

	extraNestedIndex := writeSupplyChainFile(t, workingDir, "extra-nested-index.json", makeRegistryIndex(t,
		registryPlatform{OS: "linux", Architecture: "amd64", Digest: amd64Digest},
		registryPlatform{OS: "linux", Architecture: "arm64", Digest: arm64Digest},
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: "sha256:" + strings.Repeat("8", 64),
			MediaType: "application/vnd.oci.image.index.v1+json",
		},
	))
	extraNestedEnv := make(map[string]string, len(env))
	for key, value := range env {
		extraNestedEnv[key] = value
	}
	extraNestedEnv["FAKE_SKOPEO_INDEX"] = extraNestedIndex
	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, extraNestedEnv)
	if err == nil {
		t.Fatalf("registry verifier ignored an extra duplicate-platform descriptor:\n%s", output)
	}
	if !strings.Contains(output, "platform evidence is incomplete") {
		t.Fatalf("extra duplicate-platform failure was not explicit:\n%s", output)
	}

	mislabeledNestedChild := writeSupplyChainFile(t, workingDir, "registry-mislabeled-nested-child.json", makeRegistryIndex(t,
		registryPlatform{
			OS: "linux", Architecture: "amd64", Digest: "sha256:" + strings.Repeat("9", 64),
		},
	))
	mislabeledEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		mislabeledEnv[key] = value
	}
	mislabeledEnv["FAKE_AMD64_MANIFEST"] = mislabeledNestedChild
	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, mislabeledEnv)
	if err == nil {
		t.Fatalf("registry verifier accepted descriptor-labeled nested child content:\n%s", output)
	}
	if !strings.Contains(output, "not a direct runnable image manifest") {
		t.Fatalf("mislabeled registry child failure was not explicit:\n%s", output)
	}

	wrongChildMediaType := writeSupplyChainFile(t, workingDir, "registry-wrong-child-media-type.json", []byte(
		`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json","config":{"digest":"sha256:`+
			strings.Repeat("2", 64)+`"},"layers":[]}`))
	wrongChildMediaTypeEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		wrongChildMediaTypeEnv[key] = value
	}
	wrongChildMediaTypeEnv["FAKE_AMD64_MANIFEST"] = wrongChildMediaType
	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, wrongChildMediaTypeEnv)
	if err == nil {
		t.Fatalf("registry verifier accepted a child with a non-image media type:\n%s", output)
	}
	if !strings.Contains(output, "not a direct runnable image manifest") {
		t.Fatalf("wrong registry child media-type failure was not explicit:\n%s", output)
	}

	artifactConfig := writeSupplyChainFile(t, workingDir, "registry-artifact-config-child.json", []byte(
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:`+
			strings.Repeat("4", 64)+`"},"layers":[]}`))
	artifactConfigEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		artifactConfigEnv[key] = value
	}
	artifactConfigEnv["FAKE_AMD64_MANIFEST"] = artifactConfig
	output, err = runSupplyChainCommand(t, "release/verify-registry-image-evidence.sh",
		[]string{image, publicKey, amd64Path, arm64Path}, artifactConfigEnv)
	if err == nil {
		t.Fatalf("registry verifier accepted an OCI artifact config as a runnable image:\n%s", output)
	}
	if !strings.Contains(output, "not a direct runnable image manifest") {
		t.Fatalf("registry artifact-config failure was not explicit:\n%s", output)
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
		registryPlatform{OS: "linux", Architecture: "amd64", Digest: amd64Digest},
	))
	attestation := writeSupplyChainFile(t, workingDir, "attestation.json", makeAttestationEnvelope(t, sbom))
	env := map[string]string{
		"APEXLOAD_IMAGE_INSPECTOR": "skopeo",
		"PATH":                     toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_SKOPEO_INDEX":        indexPath,
		"FAKE_AMD64_DIGEST":        amd64Digest,
		"FAKE_ATTEST_AMD64":        attestation,
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

func TestReleaseArtifactNormalizationSupportsActualGitHubArtifactLayouts(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	if !strings.Contains(string(workflowBytes), "python3 scripts/release/normalize-release-inputs.py staging") {
		t.Fatal("release workflow does not use collision-safe artifact normalization")
	}
	normalizer, err := filepath.Abs("release/normalize-release-inputs.py")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "GitHub.com upload-artifact v4",
			files: map[string]string{
				"staging/release-core-assets/apexload-server_1.2.3_linux_amd64.tar.gz":                                                   "server",
				"staging/release-client-linux-amd64/release/assets/apexctl_1.2.3_linux_amd64":                                            "apexctl",
				"staging/release-client-linux-amd64/dist/desktop/apexload-desktop-1.2.3-linux-amd64.tar.gz":                              "desktop",
				"staging/release-client-linux-amd64/dist/desktop/apexload-desktop-1.2.3-linux-amd64.deb":                                 "desktop-deb",
				"staging/release-image-controller-standard/assets/sbom-apexload-controller-standard-linux-amd64.spdx.json":               "sbom",
				"staging/release-image-controller-standard/image-metadata/controller-standard.json":                                      "metadata",
				"staging/release-image-java-vuser-runner-standard/assets/sbom-apexload-java-vuser-runner-standard-linux-amd64.spdx.json": "java-sbom",
				"staging/release-image-java-vuser-runner-standard/image-metadata/java-vuser-runner-standard.json":                        "java-metadata",
			},
		},
		{
			name: "GHES upload-artifact v3",
			files: map[string]string{
				"staging/release-core-assets/apexload-server_1.2.3_linux_arm64.tar.gz":                                       "server",
				"staging/release-client-linux-arm64/release/assets/apexctl_1.2.3_linux_arm64":                                "apexctl",
				"staging/release-client-linux-arm64/dist/desktop/apexload-desktop-1.2.3-linux-arm64.tar.gz":                  "desktop",
				"staging/release-client-linux-arm64/dist/desktop/apexload-desktop-1.2.3-linux-arm64.deb":                     "desktop-deb",
				"staging/release-image-generator-enterprise/assets/sbom-apexload-generator-enterprise-linux-arm64.spdx.json": "sbom",
				"staging/release-image-generator-enterprise/image-metadata/generator-enterprise.json":                        "metadata",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workingDir := t.TempDir()
			for _, artifact := range []string{
				"release-core-assets",
				"release-client-linux-amd64",
				"release-client-linux-arm64",
				"release-client-macos-amd64",
				"release-client-macos-arm64",
				"release-client-windows-amd64",
				"release-image-controller-standard",
				"release-image-controller-enterprise",
				"release-image-generator-standard",
				"release-image-generator-enterprise",
				"release-image-java-vuser-runner-standard",
			} {
				if err := os.MkdirAll(filepath.Join(workingDir, "staging", artifact), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range test.files {
				writeSupplyChainFile(t, workingDir, name, []byte(content))
			}
			command := exec.Command("python3", normalizer, "staging")
			command.Dir = workingDir
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("normalize artifact layout: %v\n%s", err, output)
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

func TestReleaseInputDownloaderKeepsExactArtifactsSeparated(t *testing.T) {
	content, err := os.ReadFile("../.github/actions/download-release-inputs/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	action := string(content)
	if strings.Contains(action, "pattern: release-*") || strings.Contains(action, "merge-multiple: true") {
		t.Fatal("release input downloader can overwrite colliding files before bundle normalization")
	}
	for _, name := range []string{
		"release-core-assets",
		"release-client-linux-amd64",
		"release-client-linux-arm64",
		"release-client-macos-amd64",
		"release-client-macos-arm64",
		"release-client-windows-amd64",
		"release-image-controller-standard",
		"release-image-controller-enterprise",
		"release-image-generator-standard",
		"release-image-generator-enterprise",
		"release-image-java-vuser-runner-standard",
	} {
		if got := strings.Count(action, "name: "+name); got != 2 {
			t.Errorf("exact release input %s is downloaded %d times, want once per platform path", name, got)
		}
		if got := strings.Count(action, "path: ${{ inputs.path }}/"+name); got != 2 {
			t.Errorf("release input %s uses a shared destination %d times, want separate GitHub.com/GHES paths", name, got)
		}
	}
}

func TestReleaseArtifactNormalizationRejectsDuplicateBasenames(t *testing.T) {
	workingDir := t.TempDir()
	for _, artifact := range []string{
		"release-core-assets",
		"release-client-linux-amd64",
		"release-client-linux-arm64",
		"release-client-macos-amd64",
		"release-client-macos-arm64",
		"release-client-windows-amd64",
		"release-image-controller-standard",
		"release-image-controller-enterprise",
		"release-image-generator-standard",
		"release-image-generator-enterprise",
		"release-image-java-vuser-runner-standard",
	} {
		if err := os.MkdirAll(filepath.Join(workingDir, "staging", artifact), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSupplyChainFile(t, workingDir, "staging/release-core-assets/apexctl_1.2.3_linux_amd64", []byte("first"))
	writeSupplyChainFile(t, workingDir, "staging/release-client-linux-amd64/release/assets/apexctl_1.2.3_linux_amd64", []byte("second"))
	normalizer, err := filepath.Abs("release/normalize-release-inputs.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", normalizer, "staging")
	command.Dir = workingDir
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("duplicate release artifact filenames were overwritten:\n%s", output)
	}
	if !strings.Contains(string(output), "duplicate artifact filename apexctl_1.2.3_linux_amd64") {
		t.Fatalf("collision failure was not explicit:\n%s", output)
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
	MediaType    string
	Size         int
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
		mediaType := platform.MediaType
		if mediaType == "" {
			mediaType = "application/vnd.oci.image.manifest.v1+json"
		}
		size := platform.Size
		if size == 0 {
			switch platform.Architecture {
			case "amd64":
				size = len(fakeAMD64Manifest) + 1
			case "arm64":
				size = len(fakeARM64Manifest) + 1
			default:
				size = 1
			}
		}
		manifests = append(manifests, map[string]any{
			"digest":    platform.Digest,
			"mediaType": mediaType,
			"size":      size,
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
