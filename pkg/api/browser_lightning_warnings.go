package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import ()

// lightningReloadWarning warns about browser_navigate steps that load a
// Salesforce Lightning page once the iteration's journey has started, at its
// first browser step that is not a navigation. Each reloads the page and
// restarts Lightning, so the step's time includes the framework's startup,
// which a user moving through the app never waits for again. Older recordings
// and hand edits leave them.
var lightningReloadWarning = importreview.LightningReloadWarning

// lightningAppURL reports whether target, resolved against baseURL, is a
// Lightning Experience page: /lightning/ on a Lightning or My Domain host.
var lightningAppURL = importreview.LightningAppURL
