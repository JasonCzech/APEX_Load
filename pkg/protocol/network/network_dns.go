package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func (n *Network) runDNS(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	domain := strings.TrimSpace(protocol.FirstNonEmpty(action.Target, n.host, n.address))
	if domain == "" {
		return nil, errors.New("dns query target is required")
	}
	recordType := strings.ToUpper(strings.TrimSpace(actionParamString(action.Params, "record_type")))
	if recordType == "" {
		recordType = "A"
	}
	start := time.Now()
	records := map[string]any{"record_type": recordType, "name": domain}
	switch recordType {
	case "A", "AAAA":
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(addrs))
		for _, addr := range addrs {
			if recordType == "A" && addr.IP.To4() == nil {
				continue
			}
			if recordType == "AAAA" && addr.IP.To4() != nil {
				continue
			}
			values = append(values, addr.IP.String())
		}
		sort.Strings(values)
		records["records"] = values
	case "CNAME":
		cname, err := net.DefaultResolver.LookupCNAME(ctx, domain)
		if err != nil {
			return nil, err
		}
		records["records"] = []string{cname}
	case "MX":
		mx, err := net.DefaultResolver.LookupMX(ctx, domain)
		if err != nil {
			return nil, err
		}
		values := make([]map[string]any, 0, len(mx))
		for _, item := range mx {
			values = append(values, map[string]any{"host": item.Host, "pref": item.Pref})
		}
		records["records"] = values
	case "NS":
		ns, err := net.DefaultResolver.LookupNS(ctx, domain)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(ns))
		for _, item := range ns {
			values = append(values, item.Host)
		}
		sort.Strings(values)
		records["records"] = values
	case "TXT":
		txt, err := net.DefaultResolver.LookupTXT(ctx, domain)
		if err != nil {
			return nil, err
		}
		sort.Strings(txt)
		records["records"] = txt
	default:
		return nil, fmt.Errorf("unsupported dns record_type %q", recordType)
	}
	records["record_count"] = recordCount(records["records"])
	payload := encodePayload(records)
	return &operationResult{
		payload:       payload,
		target:        domain,
		dependency:    "network_dns",
		bytesReceived: int64(len(payload)),
		dnsTime:       time.Since(start),
		messagesSent:  1,
		messagesRecv:  1,
	}, nil
}
