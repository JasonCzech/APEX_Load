package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	playwright "github.com/mxschmitt/playwright-go"
	"github.com/shirou/gopsutil/v3/process"
)

const (
	BrowserBenchmarkSchemaV2 = "apexload.browser_benchmark.v2"
	BrowserBenchmarkSchemaV3 = "apexload.browser_benchmark.v3"

	BrowserRSSAttributionCDPRendererPerVUser = "cdp_renderer_per_vuser"
	BrowserRSSAttributionUnavailable         = "unavailable"
)

// cdpProcessClassification partitions Chromium processes reported by
// browser-level CDP SystemInfo.getProcessInfo into per-VUser renderer PIDs and
// shared browser-level PIDs (browser, zygote, gpu, utility, etc.).
type cdpProcessClassification struct {
	RendererPIDs      map[int32]bool
	SharedBrowserPIDs map[int32]bool
}

// parseCDPProcessInfo decodes the raw response from CDP
// SystemInfo.getProcessInfo into renderer vs shared browser PIDs.
func parseCDPProcessInfo(raw any) (cdpProcessClassification, error) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return cdpProcessClassification{}, errors.New("cdp SystemInfo.getProcessInfo response is not an object")
	}
	items, ok := payload["processInfo"].([]any)
	if !ok {
		return cdpProcessClassification{}, errors.New("cdp SystemInfo.getProcessInfo missing processInfo array")
	}
	out := cdpProcessClassification{
		RendererPIDs:      make(map[int32]bool),
		SharedBrowserPIDs: make(map[int32]bool),
	}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return cdpProcessClassification{}, errors.New("cdp SystemInfo.getProcessInfo entry is not an object")
		}
		idVal, ok := numberFromAny(entry["id"])
		if !ok || idVal <= 0 || idVal > float64(^uint32(0)>>1) || idVal != float64(int32(idVal)) {
			return cdpProcessClassification{}, errors.New("cdp SystemInfo.getProcessInfo entry has invalid pid")
		}
		pid := int32(idVal)
		typeStr, _ := entry["type"].(string)
		procType := strings.ToLower(strings.TrimSpace(typeStr))
		if procType == "" {
			return cdpProcessClassification{}, errors.New("cdp SystemInfo.getProcessInfo entry missing type")
		}
		if procType == "renderer" {
			out.RendererPIDs[pid] = true
		} else {
			out.SharedBrowserPIDs[pid] = true
		}
	}
	if len(out.RendererPIDs) == 0 && len(out.SharedBrowserPIDs) == 0 {
		return cdpProcessClassification{}, errors.New("cdp SystemInfo.getProcessInfo returned no processes")
	}
	return out, nil
}

func queryBrowserCDPProcessInfo(pwBrowser playwright.Browser) (cdpProcessClassification, error) {
	if pwBrowser == nil {
		return cdpProcessClassification{}, errors.New("playwright browser is nil")
	}
	session, err := pwBrowser.NewBrowserCDPSession()
	if err != nil {
		return cdpProcessClassification{}, err
	}
	defer func() { _ = session.Detach() }()
	raw, err := session.Send("SystemInfo.getProcessInfo", nil)
	if err != nil {
		return cdpProcessClassification{}, err
	}
	return parseCDPProcessInfo(raw)
}

func currentPooledBrowser(engine string) playwright.Browser {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	key := browserPoolKey(strings.ToLower(strings.TrimSpace(engine)), true, browserQUICAuto)
	browser := globalBrowsers[key]
	if browser != nil && browser.IsConnected() {
		return browser
	}
	return nil
}

func snapshotBaselineRendererPIDs(engine string) map[int32]bool {
	if strings.ToLower(strings.TrimSpace(engine)) != "chromium" {
		return nil
	}
	pwBrowser := currentPooledBrowser(engine)
	if pwBrowser == nil {
		return nil
	}
	classified, err := queryBrowserCDPProcessInfo(pwBrowser)
	if err != nil {
		return nil
	}
	return classified.RendererPIDs
}

func (b *Browser) queryCDPProcessInfo() (cdpProcessClassification, error) {
	if b == nil || !b.supportsCDP() {
		return cdpProcessClassification{}, errors.New("cdp process attribution unsupported for engine")
	}
	browserCtx := b.currentBrowserContext()
	if browserCtx == nil || browserCtx.Browser() == nil {
		return cdpProcessClassification{}, errors.New("browser context unavailable")
	}
	return queryBrowserCDPProcessInfo(browserCtx.Browser())
}

// attributeVUserRendererPIDs partitions CDP-reported PIDs and descendant PIDs
// into the VUser's attributed renderer PIDs and the shared fixed-overhead PIDs
// (Playwright driver + Chromium browser, zygote, gpu, utility, and other
// non-renderer processes).
func attributeVUserRendererPIDs(baselineRendererPIDs map[int32]bool, current cdpProcessClassification, descendantPIDs []int32, driverPID int32, singleContext bool) ([]int32, []int32, error) {
	if len(current.RendererPIDs) == 0 {
		return nil, nil, errors.New("no renderer processes reported by CDP SystemInfo.getProcessInfo")
	}
	rendererSet := make(map[int32]bool)
	for pid := range current.RendererPIDs {
		if !baselineRendererPIDs[pid] {
			rendererSet[pid] = true
		}
	}
	if len(rendererSet) == 0 && singleContext {
		for pid := range current.RendererPIDs {
			rendererSet[pid] = true
		}
	}
	if len(rendererSet) == 0 {
		return nil, nil, errors.New("no renderer process attributed to browser VUser")
	}

	fixedSet := make(map[int32]bool)
	if driverPID > 0 && !current.RendererPIDs[driverPID] {
		fixedSet[driverPID] = true
	}
	for pid := range current.SharedBrowserPIDs {
		if pid > 0 && !current.RendererPIDs[pid] {
			fixedSet[pid] = true
		}
	}
	for _, pid := range descendantPIDs {
		if pid > 0 && !current.RendererPIDs[pid] {
			fixedSet[pid] = true
		}
	}
	if len(fixedSet) == 0 {
		return nil, nil, errors.New("no fixed-overhead browser or driver processes observed")
	}

	rendererPIDs := sortedInt32Keys(rendererSet)
	fixedPIDs := sortedInt32Keys(fixedSet)
	return rendererPIDs, fixedPIDs, nil
}

func sortedInt32Keys(set map[int32]bool) []int32 {
	out := make([]int32, 0, len(set))
	for pid := range set {
		out = append(out, pid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// parseLinuxSmapsPSSBytes parses Pss lines (in kB) from /proc/<pid>/smaps_rollup
// or /proc/<pid>/smaps and returns the total PSS in bytes.
func parseLinuxSmapsPSSBytes(content string) (uint64, bool) {
	var totalKB uint64
	found := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Pss:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		if len(fields) >= 3 && !strings.EqualFold(fields[2], "kB") {
			return 0, false
		}
		if ^uint64(0)/1024 < kb || ^uint64(0)-totalKB < kb {
			return 0, false
		}
		totalKB += kb
		found = true
	}
	if !found {
		return 0, false
	}
	if ^uint64(0)/1024 < totalKB {
		return 0, false
	}
	return totalKB * 1024, true
}

func readLinuxProcessPSSBytes(pid int32) (uint64, error) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return 0, errors.New("linux PSS unavailable")
	}
	for _, name := range []string{"smaps_rollup", "smaps"} {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(int(pid)), name))
		if err != nil {
			continue
		}
		if pssBytes, ok := parseLinuxSmapsPSSBytes(string(data)); ok {
			return pssBytes, nil
		}
	}
	return 0, fmt.Errorf("pss unavailable for pid %d", pid)
}

type vuserMemoryAttributionSample struct {
	PerVUserRSSBytes   *uint64
	PerVUserPSSBytes   *uint64
	FixedOverheadBytes *uint64
	TotalDescendantRSS *uint64
	MissingProcesses   int
}

func sumPIDBytes(pids []int32, read func(int32) (uint64, error)) (*uint64, int) {
	var total uint64
	measured, missing := 0, 0
	for _, pid := range pids {
		value, err := read(pid)
		if err != nil {
			missing++
			continue
		}
		if ^uint64(0)-total < value {
			return nil, len(pids)
		}
		total += value
		measured++
	}
	if measured == 0 {
		return nil, missing
	}
	return &total, missing
}

func readProcessRSSBytesByPID(pid int32) (uint64, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return 0, err
	}
	memInfo, err := p.MemoryInfo()
	if err != nil {
		return 0, err
	}
	return memInfo.RSS, nil
}

func computeAttributedVUserMemory(
	baselineRendererPIDs map[int32]bool,
	current cdpProcessClassification,
	descendantPIDs []int32,
	driverPID int32,
	singleContext bool,
	readRSS func(int32) (uint64, error),
	readPSS func(int32) (uint64, error),
) vuserMemoryAttributionSample {
	rendererPIDs, fixedPIDs, err := attributeVUserRendererPIDs(baselineRendererPIDs, current, descendantPIDs, driverPID, singleContext)
	if err != nil {
		return vuserMemoryAttributionSample{MissingProcesses: len(descendantPIDs)}
	}

	allSet := make(map[int32]bool, len(rendererPIDs)+len(fixedPIDs)+len(descendantPIDs))
	for _, pid := range rendererPIDs {
		allSet[pid] = true
	}
	for _, pid := range fixedPIDs {
		allSet[pid] = true
	}
	for _, pid := range descendantPIDs {
		if pid > 0 {
			allSet[pid] = true
		}
	}
	allPIDs := sortedInt32Keys(allSet)

	// Sample each unique PID once so per-VUser, fixed-overhead, and total
	// descendant RSS share one consistent point-in-time snapshot.
	type rssSnapshot struct {
		bytes uint64
		err   error
	}
	snapshots := make(map[int32]rssSnapshot, len(allPIDs))
	for _, pid := range allPIDs {
		val, readErr := readRSS(pid)
		snapshots[pid] = rssSnapshot{bytes: val, err: readErr}
	}
	cachedReadRSS := func(pid int32) (uint64, error) {
		snap, ok := snapshots[pid]
		if !ok {
			return 0, errors.New("pid not sampled")
		}
		return snap.bytes, snap.err
	}

	perVUserRSS, missingRenderer := sumPIDBytes(rendererPIDs, cachedReadRSS)
	fixedRSS, _ := sumPIDBytes(fixedPIDs, cachedReadRSS)
	totalDescendantRSS, missingAll := sumPIDBytes(allPIDs, cachedReadRSS)

	var perVUserPSS *uint64
	if readPSS != nil && perVUserRSS != nil && missingRenderer == 0 {
		if pss, missingPSS := sumPIDBytes(rendererPIDs, readPSS); pss != nil && missingPSS == 0 {
			perVUserPSS = pss
		}
	}

	// Require all attributed renderer PIDs to be readable so partial renderer
	// reads never underreport per-VUser RSS. Fixed-overhead and total
	// descendant sums remain available when at least one fixed process was
	// readable, with MissingProcesses > 0 recording the partial sample.
	if missingRenderer > 0 {
		perVUserRSS = nil
		perVUserPSS = nil
	}

	return vuserMemoryAttributionSample{
		PerVUserRSSBytes:   perVUserRSS,
		PerVUserPSSBytes:   perVUserPSS,
		FixedOverheadBytes: fixedRSS,
		TotalDescendantRSS: totalDescendantRSS,
		MissingProcesses:   missingAll,
	}
}
