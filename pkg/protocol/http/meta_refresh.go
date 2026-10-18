package http

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"golang.org/x/net/html"
)

type metaRefreshState struct {
	visits map[string]int
	total  int
	start  time.Time
}
type metaRefreshContextKey struct{}

func parseMetaRefresh(body []byte) (string, time.Duration, bool, error) {
	z := html.NewTokenizer(bytes.NewReader(body))
	z.SetMaxBuf(1 << 20)
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", 0, false, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			if !bytes.EqualFold(name, []byte("meta")) {
				continue
			}
			equiv, content := "", ""
			for more {
				k, v, next := z.TagAttr()
				more = next
				switch strings.ToLower(string(k)) {
				case "http-equiv":
					equiv = string(v)
				case "content":
					content = string(v)
				}
			}
			if !strings.EqualFold(strings.TrimSpace(equiv), "refresh") {
				continue
			}
			delay, target, _ := strings.Cut(content, ";")
			seconds, err := strconv.ParseFloat(strings.TrimSpace(delay), 64)
			if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 3600 {
				return "", 0, true, fmt.Errorf("invalid meta-refresh delay")
			}
			target = strings.TrimSpace(target)
			if target != "" {
				key, value, ok := strings.Cut(target, "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
					return "", 0, true, fmt.Errorf("invalid meta-refresh URL")
				}
				target = strings.Trim(strings.TrimSpace(value), "\"'")
			}
			return target, time.Duration(seconds * float64(time.Second)), true, nil
		}
	}
}
func (h *HTTP) followMetaRefresh(ctx context.Context, action *protocol.Action, req *http.Request, resp *http.Response, body []byte, result *protocol.Result) (*protocol.Result, bool) {
	if h.maxMetaRefresh == 0 || !result.Success || !h.shouldParseHTML(resp.Header) {
		return nil, false
	}
	target, delay, present, err := parseMetaRefresh(body)
	if !present {
		return nil, false
	}
	fail := func(e error) (*protocol.Result, bool) {
		result.Success = false
		result.ErrorMessage = e.Error()
		result.Duration = time.Since(result.StartTime)
		result.ProtocolEvidence = buildHTTPProtocolEvidence(action, result)
		return result, true
	}
	if err != nil {
		return fail(err)
	}
	if err := evaluateContentChecksLimited(h.contentChecks, result, string(body), h.contentCheckErrorLimit); err != nil {
		return fail(err)
	}
	base := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL
	}
	ref, err := url.Parse(target)
	if err != nil {
		return fail(fmt.Errorf("invalid meta-refresh URL"))
	}
	nextURL := base.ResolveReference(ref)
	nextURL.Fragment = ""
	if (nextURL.Scheme != "http" && nextURL.Scheme != "https") || nextURL.User != nil {
		return fail(fmt.Errorf("meta-refresh requires an HTTP(S) URL without userinfo"))
	}
	state, _ := ctx.Value(metaRefreshContextKey{}).(*metaRefreshState)
	if state == nil {
		state = &metaRefreshState{visits: map[string]int{}, start: result.StartTime}
		ctx = context.WithValue(ctx, metaRefreshContextKey{}, state)
	}
	key := nextURL.String()
	if state.total >= 50 || state.visits[key] >= h.maxMetaRefresh {
		return fail(fmt.Errorf("meta-refresh navigation limit exceeded"))
	}
	state.total++
	state.visits[key]++
	explicit, _ := ParseExplicitResources(action.Params)
	fetcher := h.resources
	if fetcher == nil && len(explicit) > 0 {
		fetcher = &resourceFetcher{h: h}
	}
	if fetcher != nil {
		fetcher.fetchPageResources(ctx, base, body, result, req.Header, explicit...)
		if !result.Success {
			return result, true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
	}
	observeVerification(ctx, resp.Header, body, false, true)
	next := *action
	next.Target = key
	next.Method = "GET"
	next.Body = nil
	next.Params = nil
	next.RedactedBody = nil
	next.RedactedTarget = ""
	// A refresh is a new GET; clear entity metadata from every header layer.
	next.Headers = refreshHeaders(action.Headers)
	next.HeaderList = refreshHeaderList(action.HeaderList)
	next.DefaultHeaders = refreshHeaders(action.DefaultHeaders)
	next.DefaultHeaderList = refreshHeaderList(action.DefaultHeaderList)
	next.RedactedHeaders = refreshHeaders(action.RedactedHeaders)
	next.RedactedHeaderList = refreshHeaderList(action.RedactedHeaderList)
	child := h.metaRefreshClient()
	child.defaultHeaders = refreshHeaders(h.defaultHeaders)
	child.typedDefaultHeaders = refreshHeaders(h.typedDefaultHeaders)
	child.defaultHeaderList = refreshHeaderList(h.defaultHeaderList)
	// Credentials belong to the original request, not the response URL after
	// HTTP redirects. A foreign response must not reattach them on refresh.
	if !sameRefreshOrigin(req.URL, nextURL) {
		next.Headers = nil
		next.HeaderList = nil
		next.DefaultHeaders = nil
		next.DefaultHeaderList = nil
		next.ResolvedDefaultHeaders = true
		next.Auth = nil
		next.RedactedAuth = nil
		next.RedactedHeaders = nil
		next.RedactedHeaderList = nil
		child.defaultHeaders = nil
		child.typedDefaultHeaders = nil
		child.defaultHeaderList = nil
		child.authConfig = nil
		child.authType = ""
		child.authHeader = ""
	}
	final, err := child.Execute(ctx, &next)
	if err != nil {
		return fail(err)
	}
	final.ContentCheckErrorMatches += result.ContentCheckErrorMatches
	final.ContentCheckInfoMatches += result.ContentCheckInfoMatches
	final.BytesSent += result.BytesSent
	final.BytesReceived += result.BytesReceived
	final.StartTime = result.StartTime
	final.Duration = time.Since(result.StartTime)
	final.DNSTime += result.DNSTime
	final.ConnectTime += result.ConnectTime
	final.TLSTime += result.TLSTime
	final.ProtocolEvidence = buildHTTPProtocolEvidence(action, final)
	return final, true
}

func sameRefreshOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func (h *HTTP) metaRefreshClient() *HTTP {
	child := &HTTP{
		receiveBufferBytes:         h.receiveBufferBytes,
		maxMetaRefresh:             h.maxMetaRefresh,
		contentCheckErrorLimit:     h.contentCheckErrorLimit,
		outcome:                    h.outcome,
		authDelay:                  h.authDelay,
		policyPools:                h.policyPools,
		connections:                h.connections,
		client:                     h.client,
		oauthClient:                h.oauthClient,
		transport:                  h.transport,
		oauthTransport:             h.oauthTransport,
		allowedTargetHosts:         h.allowedTargetHosts,
		baseURL:                    h.baseURL,
		bufPool:                    h.bufPool,
		contentChecks:              h.contentChecks,
		dataFormattingAutoDetect:   h.dataFormattingAutoDetect,
		dataFormattingChain:        h.dataFormattingChain,
		streaming:                  h.streaming,
		defaultHeaders:             h.defaultHeaders,
		typedDefaultHeaders:        h.typedDefaultHeaders,
		defaultHeaderList:          h.defaultHeaderList,
		runCorrelation:             h.runCorrelation,
		authType:                   h.authType,
		authHeader:                 h.authHeader,
		httpVersion:                h.httpVersion,
		quicSettings:               h.quicSettings,
		http3:                      h.http3,
		http3Err:                   h.http3Err,
		http3Route:                 h.http3Route,
		altSvc:                     h.altSvc,
		maxRedirects:               h.maxRedirects,
		requestTimeout:             h.requestTimeout,
		pageTimeout:                h.pageTimeout,
		htmlParseMode:              h.htmlParseMode,
		htmlExcludedMIMETypes:      h.htmlExcludedMIMETypes,
		cacheMaxUnusedIterations:   h.cacheMaxUnusedIterations,
		networkProfile:             h.networkProfile,
		traceparentEnabled:         h.traceparentEnabled,
		traceparentSampled:         h.traceparentSampled,
		authConfig:                 h.authConfig,
		authHosts:                  h.authHosts,
		page:                       h.page,
		webSockets:                 h.webSockets,
		cache:                      h.cache,
		cacheContentURLsOnly:       h.cacheContentURLsOnly,
		cacheCheckNewerVersions:    h.cacheCheckNewerVersions,
		captureCorrelationEvidence: h.captureCorrelationEvidence,
	}
	if h.resources != nil {
		r := *h.resources
		r.h = child
		child.resources = &r
	}
	return child
}

func refreshEntityHeader(name string) bool {
	return strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Transfer-Encoding")
}
func refreshHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		if !refreshEntityHeader(name) {
			out[name] = value
		}
	}
	return out
}
func refreshHeaderList(headers protocol.HeaderList) protocol.HeaderList {
	var out protocol.HeaderList
	for _, header := range headers {
		if !refreshEntityHeader(header.Name) {
			out = append(out, header)
		}
	}
	return out
}
