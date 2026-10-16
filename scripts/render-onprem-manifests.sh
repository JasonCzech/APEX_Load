#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVERLAY_DIR="$ROOT_DIR/deployments/k8s/onprem"
OUTPUT_PATH=""

usage() {
  cat <<'USAGE'
Usage: APEXLOAD_FLEET_ID=INSTALLATION_ID scripts/render-onprem-manifests.sh [--overlay-dir DIR] --output PATH

Render the edited on-prem Kustomize base or overlay with one validated fleet ID
in both the controller ConfigMap and KEDA queries. Apply the output with
kubectl apply -f PATH; kubectl apply -k does not expand environment variables.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --overlay-dir)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      OVERLAY_DIR="$2"; shift 2 ;;
    --output)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      OUTPUT_PATH="$2"; shift 2 ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

FLEET_ID_VALUE="${APEXLOAD_FLEET_ID:-}"
FLEET_ID_LOWER="$(printf '%s' "$FLEET_ID_VALUE" | tr '[:upper:]' '[:lower:]')"
if [[ "$OUTPUT_PATH" != /* || -z "$FLEET_ID_VALUE" ||
      ! "$FLEET_ID_VALUE" =~ ^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$ ||
      "$FLEET_ID_LOWER" == *change-me* ]]; then
  echo "set APEXLOAD_FLEET_ID to a unique 1-128 character installation ID and provide an absolute --output" >&2
  exit 1
fi
if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is required" >&2
  exit 1
fi
if [[ -e "$OUTPUT_PATH" || -L "$OUTPUT_PATH" ]]; then
  echo "output already exists; choose a new manifest path" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/apexload-onprem.XXXXXX")"
STAGED_OUTPUT=""
trap 'rm -rf "$TMP_DIR"; [[ -z "$STAGED_OUTPUT" ]] || rm -f "$STAGED_OUTPUT"' EXIT
kubectl kustomize "$OVERLAY_DIR" > "$TMP_DIR/rendered.yaml"
# shellcheck disable=SC2016 # the literal placeholder, not an expansion
if ! grep -Fq '${APEXLOAD_FLEET_ID}' "$TMP_DIR/rendered.yaml"; then
  echo "on-prem overlay lacks the required fleet ID placeholder" >&2
  exit 1
fi
export APEXLOAD_FLEET_ID
# Kustomize emits the placeholder as an unquoted scalar even though the base
# source quotes it. Keep the ConfigMap value a string for IDs such as "true",
# "null", or "123"; the remaining occurrences are PromQL string literals.
perl -0pi -e 's/(^[ \t]*APEXLOAD_FLEET_ID:[ \t]*)\$\{APEXLOAD_FLEET_ID\}(?=[ \t]*$)/$1"$ENV{APEXLOAD_FLEET_ID}"/mg; s/\$\{APEXLOAD_FLEET_ID\}/$ENV{APEXLOAD_FLEET_ID}/g' "$TMP_DIR/rendered.yaml"
if grep -En '\$\{[A-Za-z_][A-Za-z0-9_]*\}' "$TMP_DIR/rendered.yaml" >&2; then
  echo "rendered manifest still contains placeholders" >&2
  exit 1
fi
# Kustomize overlays are operator-editable. A placeholder elsewhere in the
# render must not mask a hard-coded fleet selector in a KEDA query.
if ! perl -0777 -ne '
  my $id = $ENV{APEXLOAD_FLEET_ID};
  my @documents = split /^---[ \t]*$/m, $_;
  my @controller = grep {
    /^kind:[ \t]*ConfigMap[ \t]*$/m && /^[ \t]*name:[ \t]*apexload-data-platform[ \t]*$/m
  } @documents;
  my @all_ids = /^[ \t]*APEXLOAD_FLEET_ID:[ \t]*(.*?)\s*$/mg;
  my @controller_ids = @controller == 1 ? ($controller[0] =~ /^[ \t]*APEXLOAD_FLEET_ID:[ \t]*(.*?)\s*$/mg) : ();
  die "controller ConfigMap fleet ID does not match the selected ID\n"
    unless @controller == 1 && @controller_ids == 1 && @all_ids == 1 &&
      $controller_ids[0] eq qq{"$id"};
  for my $object (grep { /^kind:[ \t]*ScaledObject[ \t]*$/m } @documents) {
    my $queries = 0;
    while ($object =~ /\bapexload_fleet_autoscaling\b(?:[ \t]*\{([^}]*)\})?/g) {
      $queries++;
      my $labels = $1;
      my $fleet_selectors = defined($labels) ? (() = $labels =~ /\bfleet\s*(?:=~|!~|!=|=)/g) : 0;
      die "KEDA autoscaling query fleet selector does not match the selected ID\n"
        unless defined($labels) && $fleet_selectors == 1 &&
          $labels =~ /(?:^|,)\s*fleet\s*=\s*"\Q$id\E"\s*(?:,|$)/;
    }
    die "KEDA ScaledObject has no fleet-scoped autoscaling query\n" unless $queries;
  }
' "$TMP_DIR/rendered.yaml"; then
  exit 1
fi
mkdir -p "$(dirname "$OUTPUT_PATH")"
STAGED_OUTPUT="$(mktemp "$(dirname "$OUTPUT_PATH")/.apexload-onprem.XXXXXX")"
cp "$TMP_DIR/rendered.yaml" "$STAGED_OUTPUT"
# A hard link publishes the completed file exclusively: concurrent renders
# cannot overwrite another operator's reviewed manifest.
if ! ln "$STAGED_OUTPUT" "$OUTPUT_PATH"; then
  echo "output already exists or cannot be published" >&2
  exit 1
fi
rm -f "$STAGED_OUTPUT"
STAGED_OUTPUT=""
echo "Rendered on-prem manifest: $OUTPUT_PATH"
