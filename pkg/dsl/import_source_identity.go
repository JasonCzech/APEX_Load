package dsl

import "strings"

// Import source identity: where an imported script came from, so a later
// import of the same source can update that script as a new version instead
// of creating a duplicate. It is recorded in metadata.import.preserved.
const (
	// ImportSourceDocumentKey names the source document: a collection,
	// specification, test plan or script the source declares, or the file
	// name when it declares none.
	ImportSourceDocumentKey = "source_document"
	// ImportSourceDocumentDeclaredKey is true when the document name is one
	// the source declares (a collection ID, a specification title, a test
	// plan name), false when it is only a file name, which unrelated uploads
	// can share.
	ImportSourceDocumentDeclaredKey = "source_document_declared"
	// ImportSourceItemKey names the part of the document one script came
	// from (a thread group, a protocol group), empty when the document makes
	// one script.
	ImportSourceItemKey = "source_item"
	// ImportedActionDigestKey records the action-graph digest the import
	// saved, so a later import can tell whether the script was edited since.
	ImportedActionDigestKey = "imported_action_digest"
)

// GenericImportSourceName reports whether name is one a tool gives every new
// document (springdoc's "OpenAPI definition", FastAPI's "FastAPI", NSwag's
// "My Title", Postman's "New Collection", JMeter's "Test Plan"). Unrelated
// sources share it, so it declares no identity.
func GenericImportSourceName(name string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(name), " ")) {
	case "", "api", "my api", "my title", "sample api", "untitled", "openapi definition", "fastapi",
		"swagger petstore", "new collection", "test plan", "script", "new script", "default":
		return true
	}
	return false
}

// MaxImportSourceIdentityBytes bounds each identity string.
const MaxImportSourceIdentityBytes = 512

// SetImportSourceIdentity records where script came from. An empty document
// records nothing; values past MaxImportSourceIdentityBytes are cut.
func SetImportSourceIdentity(script *Script, document string, declared bool, item string) {
	document = boundImportSourceIdentity(document)
	if script == nil || document == "" {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	preserved := script.Metadata.Import.Preserved
	preserved[ImportSourceDocumentKey] = document
	preserved[ImportSourceDocumentDeclaredKey] = declared
	if item = boundImportSourceIdentity(item); item != "" {
		preserved[ImportSourceItemKey] = item
	} else {
		delete(preserved, ImportSourceItemKey)
	}
}

// ImportSourceIdentityOf reads the identity SetImportSourceIdentity recorded.
func ImportSourceIdentityOf(metadata *ImportMetadata) (document, item string, declared, ok bool) {
	if metadata == nil || metadata.Preserved == nil {
		return "", "", false, false
	}
	document, _ = metadata.Preserved[ImportSourceDocumentKey].(string)
	document = strings.TrimSpace(document)
	if document == "" {
		return "", "", false, false
	}
	item, _ = metadata.Preserved[ImportSourceItemKey].(string)
	declared, _ = metadata.Preserved[ImportSourceDocumentDeclaredKey].(bool)
	return document, strings.TrimSpace(item), declared, true
}

func boundImportSourceIdentity(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= MaxImportSourceIdentityBytes {
		return value
	}
	cut := MaxImportSourceIdentityBytes
	for cut > 0 && !runeStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}

func runeStart(b byte) bool { return b&0xC0 != 0x80 }
