package http

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// Compatibility aliases preserve runtime callers while authoring uses httpcontract.
type AuthSupport = httpcontract.AuthSupport
type UnsupportedVersionError = httpcontract.UnsupportedVersionError
type HeaderListIssueCode = httpcontract.HeaderListIssueCode
type HeaderListIssue = httpcontract.HeaderListIssue
type HeaderListValidationError = httpcontract.HeaderListValidationError
type AuthWireValueError = httpcontract.AuthWireValueError
type AuthCapability = httpcontract.AuthCapability
type RuntimeCapabilities = httpcontract.RuntimeCapabilities

const AuthExecutable = httpcontract.AuthExecutable
const AuthReviewOnly = httpcontract.AuthReviewOnly
const AuthUnsupported = httpcontract.AuthUnsupported
const AuthTypeNone = httpcontract.AuthTypeNone
const AuthTypeInherit = httpcontract.AuthTypeInherit
const AuthTypeBasic = httpcontract.AuthTypeBasic
const AuthTypeBearer = httpcontract.AuthTypeBearer
const AuthTypeDigest = httpcontract.AuthTypeDigest
const AuthTypeNTLM = httpcontract.AuthTypeNTLM
const AuthTypeKerberos = httpcontract.AuthTypeKerberos
const AuthTypeNegotiate = httpcontract.AuthTypeNegotiate
const AuthTypeNegotiateAny = httpcontract.AuthTypeNegotiateAny
const AuthTypeAWS = httpcontract.AuthTypeAWS
const AuthTypeHawk = httpcontract.AuthTypeHawk
const VersionHTTP11 = httpcontract.VersionHTTP11
const VersionHTTP2 = httpcontract.VersionHTTP2
const VersionHTTP3 = httpcontract.VersionHTTP3
const VersionAutoH3 = httpcontract.VersionAutoH3
const HeaderListIssueInvalidName = httpcontract.HeaderListIssueInvalidName
const HeaderListIssueInvalidValue = httpcontract.HeaderListIssueInvalidValue
const HeaderListIssueDuplicateSingleton = httpcontract.HeaderListIssueDuplicateSingleton
const HeaderListIssueTransportOwned = httpcontract.HeaderListIssueTransportOwned
const HeaderListIssueHopByHopUnsupported = httpcontract.HeaderListIssueHopByHopUnsupported
const HeaderListIssueAmbiguousMap = httpcontract.HeaderListIssueAmbiguousMap
const HeaderListIssueExpectContinueUnsupported = httpcontract.HeaderListIssueExpectContinueUnsupported

var ErrHTTP10Unsupported = httpcontract.ErrHTTP10Unsupported
var ErrUnsupportedVersion = httpcontract.ErrUnsupportedVersion
var ErrHTTP2Required = httpcontract.ErrHTTP2Required
var ErrHTTP3NotEnabled = httpcontract.ErrHTTP3NotEnabled

func VersionUsesQUIC(version string) bool {
	return httpcontract.VersionUsesQUIC(version)
}
func NormalizeVersion(raw string) string {
	return httpcontract.NormalizeVersion(raw)
}
func SupportedVersions() []string {
	versions := []string{VersionHTTP11, VersionHTTP2}
	if HTTP3ExecutionError() == nil {
		versions = append(versions, VersionHTTP3)
	}
	if HTTP3UpgradeExecutionError() == nil {
		versions = append(versions, VersionAutoH3)
	}
	return versions
}
func VersionExecutable(raw string) bool {
	return httpcontract.VersionExecutable(raw)
}
func ValidateVersion(raw string) error {
	return httpcontract.ValidateVersion(raw)
}
func HeaderListIssues(headers protocol.HeaderList) []HeaderListIssue {
	return httpcontract.HeaderListIssues(headers)
}
func HeaderListIssuesForVersion(headers protocol.HeaderList, version string) []HeaderListIssue {
	return httpcontract.HeaderListIssuesForVersion(headers, version)
}
func HeaderListIssuesForVersionWithUnresolvedTemplates(headers protocol.HeaderList, version string) []HeaderListIssue {
	return httpcontract.HeaderListIssuesForVersionWithUnresolvedTemplates(headers, version)
}
func InvalidHeaderValueDetail(name, value string) string {
	return httpcontract.InvalidHeaderValueDetail(name, value)
}
func ValidateAuthWireValues(auth *protocol.AuthConfig) error {
	return httpcontract.ValidateAuthWireValues(auth)
}
func ValidateHeaderList(headers protocol.HeaderList) error {
	return httpcontract.ValidateHeaderList(headers)
}
func ValidateHeaderListForVersion(headers protocol.HeaderList, version string) error {
	return httpcontract.ValidateHeaderListForVersion(headers, version)
}
func HeaderMapIssuesForVersion(headers map[string]string, version string) []HeaderListIssue {
	return httpcontract.HeaderMapIssuesForVersion(headers, version)
}
func HeaderMapIssuesForVersionWithUnresolvedTemplates(headers map[string]string, version string) []HeaderListIssue {
	return httpcontract.HeaderMapIssuesForVersionWithUnresolvedTemplates(headers, version)
}
func EffectiveAuthoredHeaderList(defaults map[string]string, defaultList protocol.HeaderList, actionHeaders map[string]string, actionList protocol.HeaderList) (protocol.HeaderList, []HeaderListIssue) {
	return httpcontract.EffectiveAuthoredHeaderList(defaults, defaultList, actionHeaders, actionList)
}
func NormalizeMethod(raw string) string {
	return httpcontract.NormalizeMethod(raw)
}
func SupportedMethods() []string {
	return httpcontract.SupportedMethods()
}
func MethodExecutable(raw string) bool {
	return httpcontract.MethodExecutable(raw)
}
func ValidateMethod(raw string) error {
	return httpcontract.ValidateMethod(raw)
}
func ResolveMethod(explicitMethod, actionType string) string {
	return httpcontract.ResolveMethod(explicitMethod, actionType)
}
func NormalizeAuthType(raw string) string {
	return httpcontract.NormalizeAuthType(raw)
}
func LookupAuthCapability(raw string) AuthCapability {
	return httpcontract.LookupAuthCapability(raw)
}
func AuthTypeExecutable(raw string) bool {
	return httpcontract.AuthTypeExecutable(raw)
}
func ValidateAuthType(raw string) error {
	return httpcontract.ValidateAuthType(raw)
}
func ValidateTimeoutMS(value int) error {
	return httpcontract.ValidateTimeoutMS(value)
}
func ValidateMaxRedirects(value *int) error {
	return httpcontract.ValidateMaxRedirects(value)
}
func ValidateAssertions(assertions []protocol.Assertion) error {
	if len(assertions) > MaxActionAssertions {
		return fmt.Errorf("http assertions must not exceed %d", MaxActionAssertions)
	}
	for index, assertion := range assertions {
		if err := ValidateAssertionDefinition(assertion); err != nil {
			return fmt.Errorf("http assertions[%d]: %w", index, err)
		}
	}
	return nil
}
func Capabilities() RuntimeCapabilities {
	capabilities := httpcontract.Capabilities()
	capabilities.Versions = SupportedVersions()
	return capabilities
}
func effectiveAuthoredHeaderList(
	defaults map[string]string,
	defaultHeaderList protocol.HeaderList,
	actionHeaders map[string]string,
	headerList protocol.HeaderList,
) (protocol.HeaderList, []HeaderListIssue) {
	return httpcontract.EffectiveAuthoredHeaderList(defaults, defaultHeaderList, actionHeaders, headerList)
}
