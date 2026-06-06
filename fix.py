import sys

with open("pkg/protocol/browser/browser.go", "r") as f:
    lines = f.readlines()

to_remove = [
    "func normalizeOrientation",
    "func screenOrientationType",
    "func screenOrientationAngle",
    "func blockedStaticAssetPatterns",
    "func assetRequestPatterns",
    "func contentRequestPatterns",
    "func (b *Browser) fetchRequestPatterns",
    "func normalizeRecordedResources",
    "func (b *Browser) hasDownloadFilters",
    "func (b *Browser) shouldBlockFilteredRequest",
    "func (b *Browser) shouldBlockUnrecordedRequest",
    "func (b *Browser) networkThrottleAction",
    "func shouldRevalidateContentRequest",
    "func continuedRequestHeaders",
    "func (b *Browser) handleBrowserEvent",
    "func (b *Browser) handleInterceptedRequest",
    "func (b *Browser) captureClientMetrics",
    "func (b *Browser) capturePageMetrics",
    "func (b *Browser) captureRuntimeMetrics",
    "func (b *Browser) captureBrowserProcessMetrics",
    "func (b *Browser) captureBrowserProcessSnapshot",
    "func browserProcessResourceMetrics"
]

out_lines = []
skip = False
brace_count = 0

for line in lines:
    if not skip:
        for f in to_remove:
            if line.startswith(f):
                skip = True
                brace_count = 0
                break
    
    if skip:
        brace_count += line.count('{')
        brace_count -= line.count('}')
        if brace_count == 0 and '}' in line:
            skip = False
        continue
    
    out_lines.append(line)

# Let's also remove captureClientMetrics calls in Execute
out = "".join(out_lines)
out = out.replace("result.ClientMetrics = b.captureClientMetrics(actionCtx, result.Duration, processBefore)", "result.ClientMetrics = nil")

with open("pkg/protocol/browser/browser.go", "w") as f:
    f.write(out)
