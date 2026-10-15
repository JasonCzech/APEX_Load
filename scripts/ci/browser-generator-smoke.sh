#!/usr/bin/env bash
set -euo pipefail

# Launches the browser generator image's own Chromium the way an observer
# does (headless, uid 65532, read-only root, no capabilities) and checks it
# renders a page and is the build the Playwright driver pins.

if [[ $# -ne 1 || -z "${1// }" ]]; then
  echo "usage: $0 <browser-generator-image>" >&2
  exit 2
fi
image=$1

run_node() {
  docker run --rm \
    --read-only \
    --tmpfs /tmp:rw,nosuid,nodev,size=512m,mode=1777 \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --user 65532:65532 \
    --entrypoint /usr/bin/node \
    "${image}" "$@"
}

if docker run --rm --entrypoint /usr/bin/env "${image}" | grep -q '^APEXLOAD_BROWSER_EXECUTABLE='; then
  echo "the browser generator must launch Playwright's own Chromium, not APEXLOAD_BROWSER_EXECUTABLE" >&2
  exit 1
fi

expected=$(run_node -p "require('/opt/playwright-driver/package/browsers.json').browsers.find(b => b.name === 'chromium').browserVersion")
launched=$(run_node -e "
const { chromium } = require('/opt/playwright-driver/package');
(async () => {
  const browser = await chromium.launch({ args: ['--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu'] });
  const page = await browser.newPage();
  await page.setContent('<p id=\"ok\">rendered</p>');
  const text = await page.textContent('#ok');
  const version = browser.version();
  await browser.close();
  if (text !== 'rendered') throw new Error('the page did not render');
  console.log(version);
})().catch(error => { console.error(error); process.exit(1); });
")

if [[ "${expected%%.*}" != "${launched%%.*}" ]]; then
  echo "launched Chromium ${launched} is not the ${expected} build the Playwright driver pins" >&2
  exit 1
fi
echo "browser generator launched Chromium ${launched} (driver pins ${expected})"
