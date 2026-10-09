package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestApplySourceRefererHeader(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]string
		headers    map[string]string
		headerList dsl.HeaderList
		want       string
		wantList   int
	}{
		{name: "referer argument becomes the header", args: map[string]string{"Referer": "https://shop.test/home"}, want: "https://shop.test/home"},
		{name: "parameter references are converted", args: map[string]string{"Referer": "https://{Host}/home"}, want: "https://{{var.host}}/home"},
		{name: "empty referer sends none", args: map[string]string{"Referer": ""}, want: ""},
		{name: "explicit header wins", args: map[string]string{"Referer": "https://shop.test/a"}, headers: map[string]string{"referer": "https://shop.test/explicit"}, want: ""},
		{name: "ordered header list gains the entry", args: map[string]string{"Referer": "https://shop.test/home"}, headerList: dsl.HeaderList{{Name: "Accept", Value: "*/*"}}, want: "https://shop.test/home", wantList: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := &dsl.Action{Headers: tt.headers, HeaderList: tt.headerList}
			applySourceRefererHeader(action, ParsedFunction{Name: "web_url", Args: tt.args})
			if got := action.Headers["Referer"]; got != tt.want {
				t.Fatalf("Referer header = %q, want %q (headers %v)", got, tt.want, action.Headers)
			}
			if tt.wantList > 0 && len(action.HeaderList) != tt.wantList {
				t.Fatalf("header list = %v, want %d entries", action.HeaderList, tt.wantList)
			}
		})
	}
}
