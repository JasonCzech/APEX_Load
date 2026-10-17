package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
)

func SourceReviewValue(value any) *enterprisearchive.SourceReview {
	switch typed := value.(type) {
	case *enterprisearchive.SourceReview:
		return typed
	case enterprisearchive.SourceReview:
		clone := typed
		return &clone
	case map[string]any:
		review := &enterprisearchive.SourceReview{
			TargetDomain:       StringValue(typed["target_domain"]),
			SourceFiles:        SourceReviewFilesValue(typed["source_files"]),
			Mappings:           SourceReviewMappingsValue(typed["mappings"]),
			TotalSourceFiles:   IntValue(typed["total_source_files"]),
			OmittedSourceFiles: StringSliceValue(typed["omitted_source_files"]),
		}
		if len(review.SourceFiles) == 0 && len(review.Mappings) == 0 && review.TotalSourceFiles == 0 && len(review.OmittedSourceFiles) == 0 {
			return nil
		}
		return review
	default:
		return nil
	}
}

func SourceReviewFilesValue(value any) []enterprisearchive.SourceReviewFile {
	switch typed := value.(type) {
	case []enterprisearchive.SourceReviewFile:
		return append([]enterprisearchive.SourceReviewFile(nil), typed...)
	case []any:
		files := make([]enterprisearchive.SourceReviewFile, 0, len(typed))
		for _, item := range typed {
			mapping, ok := item.(map[string]any)
			if !ok {
				continue
			}
			file := enterprisearchive.SourceReviewFile{
				Path:          StringValue(mapping["path"]),
				Language:      StringValue(mapping["language"]),
				Content:       StringValue(mapping["content"]),
				OriginalBytes: IntValue(mapping["original_bytes"]),
				Truncated:     BoolValue(mapping["truncated"]),
				Redacted:      BoolValue(mapping["redacted"]),
				FullSHA256:    StringValue(mapping["full_sha256"]),
				FullBytes:     IntValue(mapping["full_bytes"]),
				FullLines:     IntValue(mapping["full_lines"]),
			}
			if file.Path == "" {
				continue
			}
			files = append(files, file)
		}
		return files
	default:
		return nil
	}
}

func SourceReviewMappingsValue(value any) []enterprisearchive.SourceReviewMapping {
	switch typed := value.(type) {
	case []enterprisearchive.SourceReviewMapping:
		return append([]enterprisearchive.SourceReviewMapping(nil), typed...)
	case []any:
		mappings := make([]enterprisearchive.SourceReviewMapping, 0, len(typed))
		for _, item := range typed {
			raw, ok := item.(map[string]any)
			if !ok {
				continue
			}
			mapping := enterprisearchive.SourceReviewMapping{
				ID:          StringValue(raw["id"]),
				SourceFile:  StringValue(raw["source_file"]),
				StartLine:   IntValue(raw["start_line"]),
				EndLine:     IntValue(raw["end_line"]),
				Function:    StringValue(raw["function"]),
				TargetPath:  StringValue(raw["target_path"]),
				TargetName:  StringValue(raw["target_name"]),
				TargetKind:  StringValue(raw["target_kind"]),
				Status:      StringValue(raw["status"]),
				SupportMode: StringValue(raw["support_mode"]),
				Notes:       StringValue(raw["notes"]),
			}
			if mapping.ID == "" && mapping.SourceFile == "" && mapping.TargetPath == "" {
				continue
			}
			mappings = append(mappings, mapping)
		}
		return mappings
	default:
		return nil
	}
}

func IntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func BoolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return ParseBoolDefault(typed, false)
	default:
		return false
	}
}
