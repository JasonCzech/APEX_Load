package recorder

import "github.com/jasonczech/apexload/pkg/scrub"

func recorderLogURL(value string) string {
	return scrub.ValueURL(value, scrub.Policy{})
}

func recorderLogError(err error) string {
	if err == nil {
		return ""
	}
	return scrub.Value(err.Error(), scrub.Policy{})
}
