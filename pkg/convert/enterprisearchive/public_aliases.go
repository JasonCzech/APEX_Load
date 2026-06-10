package enterprisearchive

// EnterpriseArchiveConvertResult is the public result shape for converting an
// enterprise script archive into APEX Load DSL or a review artifact.
type EnterpriseArchiveConvertResult = ConvertResult

// EnterpriseArchiveConvertStats reports importer coverage and review counts.
type EnterpriseArchiveConvertStats = ConvertStats

// EnterpriseArchiveConvertOptions controls enterprise archive conversion.
type EnterpriseArchiveConvertOptions = ConvertOptions

// ParsedSourceFunction is a parsed C/VUser source function call.
type ParsedSourceFunction = ParsedFunction

// ParsedSourceAction is a parsed enterprise archive lifecycle/action file.
type ParsedSourceAction = ParsedAction

// SourceProjectConfig is parsed project/runtime metadata from an archive.
type SourceProjectConfig = ProjectConfig

// SourceProtocolDetection describes protocol classification for a source archive.
type SourceProtocolDetection = DetectedProtocol

// EnterpriseArchiveReviewArtifact preserves unsupported or review-only archives.
type EnterpriseArchiveReviewArtifact = ReviewArtifact
