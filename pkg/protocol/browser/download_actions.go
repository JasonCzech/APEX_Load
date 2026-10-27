package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type browserDownloadMatcher struct {
	URLContains      string
	FilenameContains string
	FilenameRegex    *regexp.Regexp
}

func (b *Browser) downloadAction(ctx context.Context, action *protocol.Action, selector string, result *protocol.Result) error {
	matcher, err := downloadMatcherFromAction(action)
	if err != nil {
		return err
	}
	if timeoutMS := actionParamInt(action.Params, "timeout_ms", 0); timeoutMS < 0 {
		return fmt.Errorf("browser_download params.timeout_ms must be >= 0")
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	timeout := b.actionTimeout(action)
	budgetMS := max(1, int(timeout/time.Millisecond))
	budget := time.Duration(budgetMS) * time.Millisecond
	var dlClock protocol.BrowserOutcomeClock
	dlClock.Begin(budget)
	var clickErr error
	download, err := page.ExpectDownload(func() error {
		clickErr = b.clickAction(ctx, action, selector)
		return clickErr
	}, playwright.PageExpectDownloadOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, budget)),
		Predicate: func(download playwright.Download) bool {
			return downloadMatches(download, matcher)
		},
	})
	dlClock.Finish()
	if err != nil {
		switch {
		case ctx.Err() != nil || errors.Is(err, context.Canceled):
			b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
				Kind:       protocol.InteractionOutcomeKindDownload,
				Declared:   "download",
				DeadlineMS: budgetMS,
				Observed:   protocol.InteractionOutcomeNotReached,
				Reason:     "context_canceled",
			})
		case clickErr != nil && !errors.Is(clickErr, playwright.ErrTimeout) && !errors.Is(clickErr, context.DeadlineExceeded):
			b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
				Kind:       protocol.InteractionOutcomeKindDownload,
				Declared:   "download",
				DeadlineMS: budgetMS,
				Observed:   protocol.InteractionOutcomeNotReached,
				Reason:     "action_failed",
			})
		case errors.Is(err, playwright.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout"):
			if dlClock.ObservedAt.Before(dlClock.DeadlineAt) {
				dlClock.ClampToDeadline()
			}
			b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
				Kind:       protocol.InteractionOutcomeKindDownload,
				Declared:   "download",
				DeadlineMS: budgetMS,
				Observed:   protocol.InteractionOutcomeMissed,
				Reason:     "deadline_missed",
				Clock:      dlClock.Clone(),
			})
		default:
			b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
				Kind:       protocol.InteractionOutcomeKindDownload,
				Declared:   "download",
				DeadlineMS: budgetMS,
				Observed:   protocol.InteractionOutcomeNotReached,
				Reason:     "action_failed",
			})
		}
		return err
	}
	if failure := download.Failure(); failure != nil {
		b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindDownload,
			Declared:   "download",
			DeadlineMS: budgetMS,
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "download_failed",
			Clock:      dlClock.Clone(),
		})
		return failure
	}
	evidence, err := b.downloadEvidence(action, download)
	if err != nil {
		b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindDownload,
			Declared:   "download",
			DeadlineMS: budgetMS,
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "download_save_failed",
			Clock:      dlClock.Clone(),
		})
		return err
	}
	if !dlClock.ObservedAt.After(dlClock.DeadlineAt) {
		b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindDownload,
			Declared:   "download",
			DeadlineMS: budgetMS,
			Observed:   protocol.InteractionOutcomeMet,
			Clock:      dlClock.Clone(),
		})
	} else {
		b.setLastDownloadOutcome(&protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindDownload,
			Declared:   "download",
			DeadlineMS: budgetMS,
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "deadline_missed",
			Clock:      dlClock.Clone(),
		})
	}
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence["browser_download"] = evidence
	return nil
}

func downloadMatcherFromAction(action *protocol.Action) (browserDownloadMatcher, error) {
	matcher := browserDownloadMatcher{}
	if action != nil {
		matcher.URLContains = strings.TrimSpace(firstNonEmptyString(actionParamString(action.Params, "url_contains", ""), actionParamString(action.Params, "url", "")))
		matcher.FilenameContains = actionParamString(action.Params, "filename_contains", "")
		if rawRegex := actionParamString(action.Params, "filename_regex", ""); rawRegex != "" {
			compiled, err := regexp.Compile(rawRegex)
			if err != nil {
				return matcher, fmt.Errorf("browser_download params.filename_regex is invalid: %w", err)
			}
			matcher.FilenameRegex = compiled
		}
	}
	return matcher, nil
}

func downloadMatches(download playwright.Download, matcher browserDownloadMatcher) bool {
	if download == nil {
		return false
	}
	if matcher.URLContains != "" && !strings.Contains(download.URL(), matcher.URLContains) {
		return false
	}
	filename := download.SuggestedFilename()
	if matcher.FilenameContains != "" && !strings.Contains(filename, matcher.FilenameContains) {
		return false
	}
	if matcher.FilenameRegex != nil && !matcher.FilenameRegex.MatchString(filename) {
		return false
	}
	return true
}

func (b *Browser) downloadEvidence(action *protocol.Action, download playwright.Download) (map[string]any, error) {
	evidence := map[string]any{
		"url":                download.URL(),
		"suggested_filename": download.SuggestedFilename(),
	}
	savePath, err := downloadSavePath(action, download.SuggestedFilename())
	if err != nil {
		return nil, err
	}
	if savePath != "" {
		if err := os.MkdirAll(filepath.Dir(savePath), 0o755); err != nil {
			return nil, err
		}
		if err := download.SaveAs(savePath); err != nil {
			return nil, err
		}
		evidence["saved_path"] = savePath
		return evidence, nil
	}
	if path, err := download.Path(); err == nil && path != "" {
		evidence["path"] = path
	}
	return evidence, nil
}

func downloadSavePath(action *protocol.Action, suggested string) (string, error) {
	if action == nil {
		return "", nil
	}
	saveAs := actionParamString(action.Params, "save_as", "")
	saveDir := actionParamString(action.Params, "save_dir", "")
	if saveAs != "" && saveDir != "" {
		return "", fmt.Errorf("browser_download params.save_as and params.save_dir are mutually exclusive")
	}
	if saveAs != "" {
		return resolveDownloadExportPath(filepath.Dir(saveAs), filepath.Base(saveAs))
	}
	if saveDir != "" {
		return resolveDownloadExportPath(saveDir, downloadFilename(suggested))
	}
	return "", nil
}

func resolveDownloadExportPath(userDir, filename string) (string, error) {
	base := strings.TrimSpace(os.Getenv("APEXLOAD_EXPORT_DIR"))
	if base == "" {
		base = "exports"
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve browser download export base: %w", err)
	}

	safeName := downloadFilename(filename)
	rooted := filepath.Clean("/" + filepath.ToSlash(strings.TrimSpace(userDir)))
	sub := strings.TrimPrefix(rooted, "/")
	target := filepath.Clean(filepath.Join(absBase, filepath.FromSlash(sub), safeName))
	if target != absBase && !strings.HasPrefix(target, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved browser download path escapes the export directory")
	}
	return target, nil
}

func downloadFilename(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	safeName := filepath.Base(filepath.FromSlash(filename))
	if safeName == "." || safeName == ".." || safeName == string(filepath.Separator) || safeName == "" {
		return "download"
	}
	return safeName
}
