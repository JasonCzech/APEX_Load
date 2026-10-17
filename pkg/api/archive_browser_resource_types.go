package api

import "github.com/jasonczech/apexload/pkg/browserresource"

const BrowserResourcePageVersion = browserresource.BrowserResourcePageVersion
const MaxBrowserResourcePageBytes = browserresource.MaxBrowserResourcePageBytes
const MaxBrowserResourcePageItems = browserresource.MaxBrowserResourcePageItems

type BrowserResourcePagePopulation = browserresource.BrowserResourcePagePopulation
type BrowserResourcePageCoverage = browserresource.BrowserResourcePageCoverage
type BrowserResourcePageLocator = browserresource.BrowserResourcePageLocator
type BrowserResourcePageItem = browserresource.BrowserResourcePageItem
type BrowserResourcePage = browserresource.BrowserResourcePage

func browserResourceReadText(s string, limit int) bool { return browserresource.ReadText(s, limit) }
func browserResourceReadPrecision(s string) bool       { return browserresource.ReadPrecision(s) }
func browserResourceReadSHA(s string) bool             { return browserresource.ReadSHA(s) }
func ValidateBrowserResourcePage(page *BrowserResourcePage) bool {
	return browserresource.ValidateBrowserResourcePage(page)
}
func browserResourcePageItemValid(item BrowserResourcePageItem) bool {
	return browserresource.ValidatePageItem(item)
}
