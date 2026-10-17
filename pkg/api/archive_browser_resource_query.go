package api

import (
	"net/url"

	"github.com/jasonczech/apexload/pkg/browserresource"
)

const maxBrowserResourceReadEntries = browserresource.MaxReadEntries
const maxBrowserResourceRecordEntries = browserresource.MaxRecordEntries
const maxBrowserResourceCursorBytes = browserresource.MaxCursorBytes

type BrowserResourcePageQuery = browserresource.BrowserResourcePageQuery

var errBrowserResourceQuery = browserresource.ErrQuery

func ParseBrowserResourcePageQuery(values url.Values) (BrowserResourcePageQuery, error) {
	return browserresource.ParseBrowserResourcePageQuery(values)
}
func browserResourceMatches(item BrowserResourcePageItem, q BrowserResourcePageQuery) bool {
	return browserresource.Matches(item, q)
}
