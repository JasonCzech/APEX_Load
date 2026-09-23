package http

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func init() {
	if len(os.Args) != 2 || os.Args[1] != "--apexload-pac-worker" {
		return
	}
	// No filesystem, network, process, or environment APIs are exposed to JS.
	// The parent owns all DNS and network decisions and enforces a hard deadline.
	if err := limitPACWorkerMemory(); err != nil {
		os.Exit(2)
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	encoder := json.NewEncoder(os.Stdout)
	var input pacRequest
	if decoder.Decode(&input) != nil || len(input.Source) > maxPACBytes || len(input.URL) > 4096 || validatePACSubset(input.Source) != nil {
		os.Exit(2)
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(128)
	timer := time.AfterFunc(500*time.Millisecond, func() { vm.Interrupt("PAC CPU limit") })
	defer timer.Stop()
	_ = vm.Set("dnsResolve", func(host string) string {
		if len(host) > 253 || strings.ContainsAny(host, "/\\\x00\r\n") {
			panic(vm.NewTypeError("invalid DNS host"))
		}
		if encoder.Encode(pacResponse{DNS: host}) != nil {
			panic(vm.NewTypeError("DNS unavailable"))
		}
		var ip string
		if decoder.Decode(&ip) != nil {
			panic(vm.NewTypeError("DNS unavailable"))
		}
		return ip
	})
	_ = vm.Set("shExpMatch", func(s, p string) bool {
		if len(s) > 4096 || len(p) > 4096 {
			panic(vm.NewTypeError("PAC glob exceeds limit"))
		}
		pattern := regexp.QuoteMeta(p)
		pattern = strings.ReplaceAll(pattern, `\*`, ".*")
		pattern = strings.ReplaceAll(pattern, `\?`, ".")
		re, e := regexp.Compile("^(?:" + pattern + ")$")
		return e == nil && re.MatchString(s)
	})
	_, err := vm.RunString(`
 function isPlainHostName(h){return h.indexOf('.')<0;}
 function dnsDomainIs(h,d){return h.slice(-d.length)===d;}
 function localHostOrDomainIs(h,d){return h===d || (isPlainHostName(h) && d.indexOf(h+'.')===0);}
 function dnsDomainLevels(h){return h.split('.').length-1;}
 function isResolvable(h){return dnsResolve(h)!=='';}
 function isInNet(h,p,m){var a=dnsResolve(h).split('.'),b=p.split('.'),c=m.split('.');if(a.length!==4||b.length!==4||c.length!==4)return false;for(var i=0;i<4;i++)if((+a[i]&+c[i])!== (+b[i]&+c[i]))return false;return true;}
 `)
	if err == nil {
		_, err = vm.RunString(input.Source)
	}
	result := ""
	if err == nil {
		fn, ok := goja.AssertFunction(vm.Get("FindProxyForURL"))
		if !ok {
			err = fmt.Errorf("missing PAC entrypoint")
		} else {
			var value goja.Value
			value, err = fn(goja.Undefined(), vm.ToValue(input.URL), vm.ToValue(input.Host))
			if err == nil {
				result = value.String()
			}
		}
	}
	if err != nil || len(result) > 4096 {
		_ = encoder.Encode(pacResponse{Error: "evaluation failed"})
		os.Exit(1)
	}
	_ = encoder.Encode(pacResponse{Result: result})
	os.Exit(0)
}
