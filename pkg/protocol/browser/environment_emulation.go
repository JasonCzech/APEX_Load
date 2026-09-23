package browser

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Only defines explicit emulation properties. This does not mutate viewport,
// screen dimensions, HTTP language negotiation, or the host operating system.
func (b *Browser) environmentEmulationScript() string {
	var script strings.Builder
	if b.systemLanguage != "" {
		value, _ := json.Marshal(b.systemLanguage)
		fmt.Fprintf(&script, "Object.defineProperty(Navigator.prototype, 'systemLanguage', {configurable:true,get:()=>%s});\n", value)
	}
	if b.availableScreenWidth > 0 {
		fmt.Fprintf(&script, "Object.defineProperty(Screen.prototype, 'availWidth', {configurable:true,get:()=>%d});\n", b.availableScreenWidth)
	}
	if b.availableScreenHeight > 0 {
		fmt.Fprintf(&script, "Object.defineProperty(Screen.prototype, 'availHeight', {configurable:true,get:()=>%d});\n", b.availableScreenHeight)
	}
	return script.String()
}
