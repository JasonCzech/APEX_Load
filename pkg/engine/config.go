package engine

import "github.com/jasonczech/stormrunner/pkg/dsl"

func buildProtoConfig(script *dsl.Script) map[string]any {
	cfg := map[string]any{
		"base_url": script.Config.BaseURL,
	}

	httpCfg := script.Config.HTTP
	soapCfg := script.Config.SOAP

	// Timeouts
	if httpCfg.ConnectTimeoutMs > 0 {
		cfg["connect_timeout_ms"] = float64(httpCfg.ConnectTimeoutMs)
	}
	if httpCfg.RequestTimeoutMs > 0 {
		cfg["request_timeout_ms"] = float64(httpCfg.RequestTimeoutMs)
	}
	if httpCfg.TLSTimeoutMs > 0 {
		cfg["tls_timeout_ms"] = float64(httpCfg.TLSTimeoutMs)
	}
	if httpCfg.IdleTimeoutMs > 0 {
		cfg["idle_timeout_ms"] = float64(httpCfg.IdleTimeoutMs)
	}

	// Connection
	if httpCfg.MaxConnsPerHost > 0 {
		cfg["max_conns_per_host"] = float64(httpCfg.MaxConnsPerHost)
	}
	if httpCfg.MaxIdleConns > 0 {
		cfg["max_idle_conns"] = float64(httpCfg.MaxIdleConns)
	}
	if httpCfg.KeepAlive != nil {
		cfg["keep_alive"] = *httpCfg.KeepAlive
	}
	if httpCfg.Version != "" {
		cfg["http_version"] = httpCfg.Version
	}
	if httpCfg.MaxRedirects != nil {
		cfg["max_redirects"] = float64(*httpCfg.MaxRedirects)
	}
	if httpCfg.CookiesEnabled != nil {
		cfg["cookies_enabled"] = *httpCfg.CookiesEnabled
	}

	// Browser emulation headers
	if httpCfg.UserAgent != "" {
		cfg["user_agent"] = httpCfg.UserAgent
	}
	if httpCfg.AcceptEncoding != "" {
		cfg["accept_encoding"] = httpCfg.AcceptEncoding
	}
	if httpCfg.AcceptLanguage != "" {
		cfg["accept_language"] = httpCfg.AcceptLanguage
	}
	if len(httpCfg.DefaultHeaders) > 0 {
		cfg["default_headers"] = httpCfg.DefaultHeaders
	}
	if soapCfg.Version != "" {
		cfg["soap_version"] = soapCfg.Version
	}
	if len(soapCfg.Namespaces) > 0 {
		cfg["soap_namespaces"] = soapCfg.Namespaces
	}
	if ws := soapCfg.WSSecurity; ws != nil {
		if token := ws.UsernameToken; token != nil {
			if token.Username != "" {
				cfg["soap_ws_security_username"] = token.Username
			}
			if token.Password != "" {
				cfg["soap_ws_security_password"] = token.Password
			}
			if token.PasswordType != "" {
				cfg["soap_ws_security_password_type"] = token.PasswordType
			}
			if token.Nonce {
				cfg["soap_ws_security_nonce"] = true
			}
			if token.Created {
				cfg["soap_ws_security_created"] = true
			}
		}
		if timestamp := ws.Timestamp; timestamp != nil {
			cfg["soap_ws_security_timestamp_ttl_seconds"] = float64(timestamp.TTLSeconds)
		}
	}
	if soapCfg.FaultHandling.FailOnFault {
		cfg["soap_fault_fail_on_fault"] = true
	}

	browserCfg := script.Config.Browser
	if browserCfg.Engine != "" {
		cfg["browser_engine"] = browserCfg.Engine
	}
	if browserCfg.Headless != nil {
		cfg["browser_headless"] = *browserCfg.Headless
	}
	if browserCfg.SlowMoMs > 0 {
		cfg["browser_slow_mo_ms"] = float64(browserCfg.SlowMoMs)
	}
	if browserCfg.UserAgent != "" {
		cfg["browser_user_agent"] = browserCfg.UserAgent
	}
	if browserCfg.CaptureScreenshots != "" {
		cfg["browser_capture_screenshots"] = browserCfg.CaptureScreenshots
	}
	if browserCfg.Viewport.Width > 0 {
		cfg["browser_viewport_width"] = float64(browserCfg.Viewport.Width)
	}
	if browserCfg.Viewport.Height > 0 {
		cfg["browser_viewport_height"] = float64(browserCfg.Viewport.Height)
	}

	jmsCfg := script.Config.JMS
	if jmsCfg.Provider != "" {
		cfg["jms_provider"] = jmsCfg.Provider
	}
	if jmsCfg.BrokerURL != "" {
		cfg["broker_url"] = jmsCfg.BrokerURL
	}
	if jmsCfg.MessageVPN != "" {
		cfg["jms_message_vpn"] = jmsCfg.MessageVPN
	}
	if jmsCfg.Username != "" {
		cfg["jms_username"] = jmsCfg.Username
	}
	if jmsCfg.Password != "" {
		cfg["jms_password"] = jmsCfg.Password
	}
	if jmsCfg.ClientID != "" {
		cfg["jms_client_id"] = jmsCfg.ClientID
	}
	if jmsCfg.Destination != "" {
		cfg["jms_destination"] = jmsCfg.Destination
	}
	if jmsCfg.DestinationType != "" {
		cfg["jms_destination_type"] = jmsCfg.DestinationType
	}
	if jmsCfg.ReplyTo != "" {
		cfg["jms_reply_to"] = jmsCfg.ReplyTo
	}
	if jmsCfg.Acknowledgement != "" {
		cfg["jms_acknowledgement"] = jmsCfg.Acknowledgement
	}
	if jmsCfg.DeliveryMode != "" {
		cfg["jms_delivery_mode"] = jmsCfg.DeliveryMode
	}
	if jmsCfg.TimeoutMs > 0 {
		cfg["jms_timeout_ms"] = float64(jmsCfg.TimeoutMs)
	}
	if len(jmsCfg.Properties) > 0 {
		cfg["jms_properties"] = jmsCfg.Properties
	}
	if jmsCfg.Mock {
		cfg["jms_mock"] = true
	}

	// TLS
	tls := httpCfg.TLS
	if tls.MinVersion != "" {
		cfg["tls_min_version"] = tls.MinVersion
	}
	if tls.MaxVersion != "" {
		cfg["tls_max_version"] = tls.MaxVersion
	}
	if tls.InsecureSkipVerify {
		cfg["tls_insecure_skip_verify"] = true
	}
	if tls.ClientCertFile != "" {
		cfg["tls_client_cert_file"] = tls.ClientCertFile
	}
	if tls.ClientKeyFile != "" {
		cfg["tls_client_key_file"] = tls.ClientKeyFile
	}
	if tls.CACertFile != "" {
		cfg["tls_ca_cert_file"] = tls.CACertFile
	}

	// Proxy
	proxy := httpCfg.Proxy
	if proxy.URL != "" {
		cfg["proxy_url"] = proxy.URL
	}
	if proxy.Username != "" {
		cfg["proxy_username"] = proxy.Username
	}
	if proxy.Password != "" {
		cfg["proxy_password"] = proxy.Password
	}
	if len(proxy.NoProxy) > 0 {
		cfg["proxy_no_proxy"] = proxy.NoProxy
	}

	// Auth
	auth := httpCfg.Auth
	if auth.Type != "" {
		cfg["auth_type"] = auth.Type
	}
	if auth.Username != "" {
		cfg["auth_username"] = auth.Username
	}
	if auth.Password != "" {
		cfg["auth_password"] = auth.Password
	}
	if auth.Token != "" {
		cfg["auth_token"] = auth.Token
	}
	if auth.Domain != "" {
		cfg["auth_domain"] = auth.Domain
	}

	return cfg
}
