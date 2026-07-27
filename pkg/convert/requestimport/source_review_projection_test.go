package requestimport

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
)

func TestRedactSourceReviewFilesByBaseName(t *testing.T) {
	t.Parallel()
	review := &sourceview.Review{SourceFiles: []sourceview.SourceFile{
		{Path: "fixtures/users.dat", Content: "synthetic-user"},
		{Path: `fixtures\Password.dat`, Content: "source-password-canary"},
	}}

	RedactSourceReviewFilesByBaseName(review, []string{"PASSWORD.DAT"})

	if strings.Contains(review.SourceFiles[1].Content, "source-password-canary") ||
		!review.SourceFiles[1].Redacted {
		t.Fatalf("protected source file = %#v", review.SourceFiles[1])
	}
	if review.SourceFiles[0].Content != "synthetic-user" ||
		review.SourceFiles[0].Redacted {
		t.Fatalf("nonsensitive source file = %#v", review.SourceFiles[0])
	}
}
