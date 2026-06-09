#!/usr/bin/env bash
set -euo pipefail

repo_url="${LR_SCRIPTS_REPO_URL:-https://github.com/salvadorcamachogithub/LR_Scripts.git}"
commit="${LR_SCRIPTS_COMMIT:-cc010f01ea6f0120d1e22bc1acb41522bf98d03d}"
dest="${1:-${LR_SCRIPTS_DEST:-/tmp/apexload-lr-scripts-corpus}}"

if [[ -d "${dest}/.git" ]]; then
  git -C "${dest}" fetch --depth 1 origin "${commit}"
else
  rm -rf "${dest}"
  git clone --filter=blob:none --no-checkout "${repo_url}" "${dest}"
  git -C "${dest}" sparse-checkout init --cone
  git -C "${dest}" sparse-checkout set Scripts
  git -C "${dest}" fetch --depth 1 origin "${commit}"
fi

git -C "${dest}" checkout --detach "${commit}"
git -C "${dest}" sparse-checkout set Scripts

printf 'Fetched LR_Scripts corpus at %s into %s\n' "${commit}" "${dest}"
