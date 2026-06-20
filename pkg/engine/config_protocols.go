package engine

import "github.com/jasonczech/apexload/pkg/dsl"

// Per-protocol config appenders extracted from buildProtoConfigWithSecretResolver.
// Each reads one protocol section off the script and writes the flattened keys into
// cfg; secret resolution happens once in the orchestrator.

func appendHTTPConfig(cfg map[string]any, script *dsl.Script) {
	httpCfg := script.Config.HTTP
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
	if len(script.Config.WebRuntime.ContentChecks) > 0 {
		cfg["web_content_checks"] = flattenContentChecks(script.Config.WebRuntime.ContentChecks)
	}
	if filters := script.Config.WebRuntime.DownloadFilters; filters.Mode != "" {
		cfg["web_download_filters_mode"] = filters.Mode
		if len(filters.Hosts) > 0 {
			cfg["web_download_filters_hosts"] = filters.Hosts
		}
	}
	if dataFormatting := script.Config.WebRuntime.DataFormatting; dataFormatting.AutoDetect || len(dataFormatting.Chain) > 0 {
		if dataFormatting.AutoDetect {
			cfg["web_data_formatting_auto_detect"] = true
		}
		if len(dataFormatting.Chain) > 0 {
			cfg["web_data_formatting_chain"] = flattenDFEChain(dataFormatting.Chain)
		}
	}
	if networkCfg := script.Config.WebRuntime.NetworkSimulation; networkCfg.Mode != "" {
		cfg["web_network_simulation_mode"] = networkCfg.Mode
		if networkCfg.Preset != "" {
			cfg["web_network_simulation_preset"] = networkCfg.Preset
		}
		if networkCfg.BandwidthKbps > 0 {
			cfg["web_network_simulation_bandwidth_kbps"] = float64(networkCfg.BandwidthKbps)
		}
		if networkCfg.LatencyMs > 0 {
			cfg["web_network_simulation_latency_ms"] = float64(networkCfg.LatencyMs)
		}
		if networkCfg.JitterMs > 0 {
			cfg["web_network_simulation_jitter_ms"] = float64(networkCfg.JitterMs)
		}
		if networkCfg.PacketLossPct > 0 {
			cfg["web_network_simulation_packet_loss_pct"] = networkCfg.PacketLossPct
		}
	}
	if pool := normalizeSourceIPs(script.Config.WebRuntime.SourceIPs); len(pool) > 0 {
		cfg["web_source_ips"] = pool
	}
	if profiles := normalizeNetworkProfiles(script.Config.WebRuntime.NetworkProfiles); len(profiles) > 0 {
		cfg["web_network_profiles"] = profiles
	}
	if streamingCfg := script.Config.WebRuntime.Streaming; streamingCfg.Mode != "" || streamingCfg.BufferSizeSeconds > 0 || streamingCfg.FixedBitrateKbps > 0 || streamingCfg.BandwidthEstimation != "" {
		if streamingCfg.Mode != "" {
			cfg["web_streaming_mode"] = streamingCfg.Mode
		}
		if streamingCfg.BufferSizeSeconds > 0 {
			cfg["web_streaming_buffer_size_seconds"] = float64(streamingCfg.BufferSizeSeconds)
		}
		if streamingCfg.FixedBitrateKbps > 0 {
			cfg["web_streaming_fixed_bitrate_kbps"] = float64(streamingCfg.FixedBitrateKbps)
		}
		if streamingCfg.BandwidthEstimation != "" {
			cfg["web_streaming_bandwidth_estimation"] = streamingCfg.BandwidthEstimation
		}
	}
}

func appendWebSocketConfig(cfg map[string]any, script *dsl.Script) {
	webSocketCfg := script.Config.WebSocket
	if webSocketCfg.ReadTimeoutMs > 0 {
		cfg["websocket_read_timeout_ms"] = float64(webSocketCfg.ReadTimeoutMs)
	}
	if webSocketCfg.WriteTimeoutMs > 0 {
		cfg["websocket_write_timeout_ms"] = float64(webSocketCfg.WriteTimeoutMs)
	}
	if webSocketCfg.MaxMessageSize > 0 {
		cfg["websocket_max_message_size"] = float64(webSocketCfg.MaxMessageSize)
	}
	if webSocketCfg.Subprotocol != "" {
		cfg["websocket_subprotocol"] = webSocketCfg.Subprotocol
	}
	if webSocketCfg.Origin != "" {
		cfg["websocket_origin"] = webSocketCfg.Origin
	}
	if webSocketCfg.Compression {
		cfg["websocket_compression"] = true
	}
	if webSocketCfg.Reconnect {
		cfg["websocket_reconnect"] = true
	}
}

func appendGRPCConfig(cfg map[string]any, script *dsl.Script) {
	grpcCfg := script.Config.GRPC
	if len(grpcCfg.ProtoFiles) > 0 {
		cfg["grpc_proto_files"] = grpcCfg.ProtoFiles
	}
	if grpcCfg.UseReflection {
		cfg["grpc_use_reflection"] = true
	}
	if grpcCfg.PlainText {
		cfg["grpc_plain_text"] = true
	}
	if grpcCfg.MaxRecvMsgSize > 0 {
		cfg["grpc_max_recv_msg_size"] = float64(grpcCfg.MaxRecvMsgSize)
	}
	if grpcCfg.TimeoutMs > 0 {
		cfg["grpc_timeout_ms"] = float64(grpcCfg.TimeoutMs)
	}
	if len(grpcCfg.Metadata) > 0 {
		cfg["grpc_metadata"] = grpcCfg.Metadata
	}
	if grpcCfg.Authority != "" {
		cfg["grpc_authority"] = grpcCfg.Authority
	}
}

func appendJavaHTTPConfig(cfg map[string]any, script *dsl.Script) {
	javaHTTPCfg := script.Config.JavaHTTP
	if javaHTTPCfg.Codec != "" {
		cfg["java_http_codec"] = javaHTTPCfg.Codec
	}
	if javaHTTPCfg.HelperMode != "" {
		cfg["java_http_helper_mode"] = javaHTTPCfg.HelperMode
	}
	if javaHTTPCfg.ContentType != "" {
		cfg["java_http_content_type"] = javaHTTPCfg.ContentType
	}
	if javaHTTPCfg.Accept != "" {
		cfg["java_http_accept"] = javaHTTPCfg.Accept
	}
	if javaHTTPCfg.BodyEncoding != "" {
		cfg["java_http_body_encoding"] = javaHTTPCfg.BodyEncoding
	}
	if javaHTTPCfg.ObjectSchema != "" {
		cfg["java_http_object_schema"] = javaHTTPCfg.ObjectSchema
	}
	if len(javaHTTPCfg.RequestMapping) > 0 {
		cfg["java_http_request_mapping"] = javaHTTPCfg.RequestMapping
	}
	if len(javaHTTPCfg.ResponseMapping) > 0 {
		cfg["java_http_response_mapping"] = javaHTTPCfg.ResponseMapping
	}
	if javaHTTPCfg.ExtractorType != "" {
		cfg["java_http_extractor_type"] = javaHTTPCfg.ExtractorType
	}
	if javaHTTPCfg.PreserveBinaryResult {
		cfg["java_http_preserve_binary_result"] = true
	}
}

func appendJavaVuserConfig(cfg map[string]any, script *dsl.Script) {
	javaVuserCfg := script.Config.JavaVuser
	// Java Vuser enablement and the runner command/classpath/working-directory
	// are operator-owned: the protocol resolves them from process environment
	// (APEXLOAD_JAVA_VUSER_*), never from uploaded script DSL. Deliberately do
	// NOT flatten javaVuserCfg.Enabled/RunnerCommand/HarnessJar/Classpath/
	// WorkDir/MaxProcesses here — doing so would let a script author enable and
	// steer local process execution on the generator. Only the non-privileged
	// per-action timeouts remain script-controlled.
	if javaVuserCfg.StartupTimeoutMs > 0 {
		cfg["java_vuser_startup_timeout_ms"] = float64(javaVuserCfg.StartupTimeoutMs)
	}
	if javaVuserCfg.ActionTimeoutMs > 0 {
		cfg["java_vuser_action_timeout_ms"] = float64(javaVuserCfg.ActionTimeoutMs)
	}
}

func appendSOAPConfig(cfg map[string]any, script *dsl.Script) {
	soapCfg := script.Config.SOAP
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
}

func appendDatabaseConfig(cfg map[string]any, script *dsl.Script) {
	databaseCfg := script.Config.Database
	if databaseCfg.Driver != "" {
		cfg["database_driver"] = databaseCfg.Driver
	}
	if databaseCfg.DSN != "" {
		cfg["database_dsn"] = databaseCfg.DSN
	}
	if databaseCfg.DSNSecret != "" {
		cfg["database_dsn_secret"] = databaseCfg.DSNSecret
	}
	if databaseCfg.MaxOpenConns > 0 {
		cfg["database_max_open_conns"] = float64(databaseCfg.MaxOpenConns)
	}
	if databaseCfg.MaxIdleConns > 0 {
		cfg["database_max_idle_conns"] = float64(databaseCfg.MaxIdleConns)
	}
	if databaseCfg.ConnMaxLifetimeMs > 0 {
		cfg["database_conn_max_lifetime_ms"] = float64(databaseCfg.ConnMaxLifetimeMs)
	}
	if databaseCfg.QueryTimeoutMs > 0 {
		cfg["database_query_timeout_ms"] = float64(databaseCfg.QueryTimeoutMs)
	}
	if databaseCfg.MaxRows > 0 {
		cfg["database_max_rows"] = float64(databaseCfg.MaxRows)
	}
	if databaseCfg.ReadOnly {
		cfg["database_read_only"] = true
	}
	if databaseCfg.RollbackOnTeardown {
		cfg["database_rollback_on_teardown"] = true
	}
}

func appendNetworkConfig(cfg map[string]any, script *dsl.Script) {
	networkCfg := script.Config.Network
	if networkCfg.Subprotocol != "" {
		cfg["network_subprotocol"] = networkCfg.Subprotocol
	}
	if networkCfg.Host != "" {
		cfg["network_host"] = networkCfg.Host
	}
	if networkCfg.Address != "" {
		cfg["network_address"] = networkCfg.Address
	}
	if networkCfg.Port > 0 {
		cfg["network_port"] = float64(networkCfg.Port)
	}
	if networkCfg.TimeoutMs > 0 {
		cfg["network_timeout_ms"] = float64(networkCfg.TimeoutMs)
	}
	if networkCfg.TLS {
		cfg["network_tls"] = true
	}
	if networkCfg.Username != "" {
		cfg["network_username"] = networkCfg.Username
	}
	if networkCfg.Password != "" {
		cfg["network_password"] = networkCfg.Password
	}
	if networkCfg.From != "" {
		cfg["network_from"] = networkCfg.From
	}
	if len(networkCfg.To) > 0 {
		cfg["network_to"] = networkCfg.To
	}
	if networkCfg.BaseDN != "" {
		cfg["network_base_dn"] = networkCfg.BaseDN
	}
}

func appendBrowserConfig(cfg map[string]any, script *dsl.Script) {
	browserCfg := script.Config.Browser
	if browserCfg.Engine != "" {
		cfg["browser_engine"] = browserCfg.Engine
	}
	if browserCfg.Headless != nil {
		cfg["browser_headless"] = *browserCfg.Headless
	}
	if browserCfg.CacheEnabled != nil {
		cfg["browser_cache_enabled"] = *browserCfg.CacheEnabled
	}
	if browserCfg.CacheContentURLs != nil {
		cfg["browser_cache_content_urls"] = *browserCfg.CacheContentURLs
	}
	if browserCfg.CheckNewerVersions != nil {
		cfg["browser_check_newer_versions"] = *browserCfg.CheckNewerVersions
	}
	if browserCfg.DownloadResources != "" {
		cfg["browser_download_resources"] = browserCfg.DownloadResources
	}
	if browserCfg.DownloadResources == "recorded" {
		if recordedResources, ok := recordedResourceURLs(script); ok {
			cfg["browser_recorded_resources_available"] = true
			cfg["browser_recorded_resources"] = recordedResources
		}
	}
	if browserCfg.SlowMoMs > 0 {
		cfg["browser_slow_mo_ms"] = float64(browserCfg.SlowMoMs)
	}
	if browserCfg.UserAgent != "" {
		cfg["browser_user_agent"] = browserCfg.UserAgent
	}
	if browserCfg.DeviceProfile != "" {
		cfg["browser_device_profile"] = browserCfg.DeviceProfile
	}
	if browserCfg.TouchMode != nil {
		cfg["browser_touch_mode"] = *browserCfg.TouchMode
	}
	if browserCfg.Orientation != "" {
		cfg["browser_orientation"] = browserCfg.Orientation
	}
	if browserCfg.ClientMetrics != nil {
		cfg["browser_client_metrics"] = *browserCfg.ClientMetrics
	}
	if browserCfg.ExperienceCanary {
		cfg["browser_experience_canary"] = true
	}
	if browserCfg.Traceparent != nil {
		cfg["browser_traceparent"] = *browserCfg.Traceparent
	}
	if browserCfg.HARCapture != "" {
		cfg["browser_har_capture"] = browserCfg.HARCapture
	}
	if browserCfg.HARCapturePolicy != "" {
		cfg["browser_har_capture_policy"] = browserCfg.HARCapturePolicy
	}
	if browserCfg.HARSLAThresholdMs > 0 {
		cfg["browser_har_sla_breach_ms"] = float64(browserCfg.HARSLAThresholdMs)
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
}

func appendJMSConfig(cfg map[string]any, script *dsl.Script) {
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
	if jmsCfg.Subscription != "" {
		cfg["jms_subscription"] = jmsCfg.Subscription
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
	if jmsCfg.TLS != nil {
		if jmsCfg.TLS.CACertSecret != "" {
			cfg["jms_tls_ca_cert_secret"] = jmsCfg.TLS.CACertSecret
		}
		if jmsCfg.TLS.TrustStoreRef != "" {
			cfg["jms_tls_trust_store_ref"] = jmsCfg.TLS.TrustStoreRef
		}
		if jmsCfg.TLS.InsecureSkipVerify {
			cfg["jms_tls_insecure_skip_verify"] = true
		}
	}
	if jmsCfg.Mock {
		cfg["jms_mock"] = true
	}
}

func appendKafkaConfig(cfg map[string]any, script *dsl.Script) {
	kafkaCfg := script.Config.Kafka
	if len(kafkaCfg.Brokers) > 0 {
		cfg["kafka_brokers"] = kafkaCfg.Brokers
	}
	if kafkaCfg.Profile != "" {
		cfg["kafka_profile"] = kafkaCfg.Profile
	}
	if kafkaCfg.MaxMessageBytes > 0 {
		cfg["kafka_max_message_bytes"] = float64(kafkaCfg.MaxMessageBytes)
	}
	if kafkaCfg.SecurityProtocol != "" {
		cfg["kafka_security_protocol"] = kafkaCfg.SecurityProtocol
	}
	if kafkaCfg.SASLMechanism != "" {
		cfg["kafka_sasl_mechanism"] = kafkaCfg.SASLMechanism
	}
	if kafkaCfg.SASLUsername != "" {
		cfg["kafka_sasl_username"] = kafkaCfg.SASLUsername
	}
	if kafkaCfg.SASLPassword != "" {
		cfg["kafka_sasl_password"] = kafkaCfg.SASLPassword
	}
	if kafkaCfg.SASLPasswordSecret != "" {
		cfg["kafka_sasl_password_secret"] = kafkaCfg.SASLPasswordSecret
	}
	if kafkaCfg.TLS != nil {
		if kafkaCfg.TLS.InsecureSkipVerify {
			cfg["kafka_tls_insecure_skip_verify"] = true
		}
		if kafkaCfg.TLS.ClientCertFile != "" {
			cfg["kafka_tls_client_cert_file"] = kafkaCfg.TLS.ClientCertFile
		}
		if kafkaCfg.TLS.ClientKeyFile != "" {
			cfg["kafka_tls_client_key_file"] = kafkaCfg.TLS.ClientKeyFile
		}
		if kafkaCfg.TLS.CACertFile != "" {
			cfg["kafka_tls_ca_cert_file"] = kafkaCfg.TLS.CACertFile
		}
	}
	if kafkaCfg.ClientID != "" {
		cfg["kafka_client_id"] = kafkaCfg.ClientID
	}
	if kafkaCfg.ConsumerGroup != "" {
		cfg["kafka_consumer_group"] = kafkaCfg.ConsumerGroup
	}
	if kafkaCfg.Topic != "" {
		cfg["kafka_topic"] = kafkaCfg.Topic
	}
	if kafkaCfg.Acks != "" {
		cfg["kafka_acks"] = kafkaCfg.Acks
	}
	if kafkaCfg.Idempotence != "" {
		cfg["kafka_idempotence"] = kafkaCfg.Idempotence
	}
	if kafkaCfg.TransactionalID != "" {
		cfg["kafka_transactional_id"] = kafkaCfg.TransactionalID
	}
	if kafkaCfg.IsolationLevel != "" {
		cfg["kafka_isolation_level"] = kafkaCfg.IsolationLevel
	}
	if kafkaCfg.Compression != "" {
		cfg["kafka_compression"] = kafkaCfg.Compression
	}
	if kafkaCfg.PartitionStrategy != "" {
		cfg["kafka_partition_strategy"] = kafkaCfg.PartitionStrategy
	}
	if kafkaCfg.AutoOffsetReset != "" {
		cfg["kafka_auto_offset_reset"] = kafkaCfg.AutoOffsetReset
	}
	if kafkaCfg.Partitions > 0 {
		cfg["kafka_partitions"] = float64(kafkaCfg.Partitions)
	}
	if kafkaCfg.TimeoutMs > 0 {
		cfg["kafka_timeout_ms"] = float64(kafkaCfg.TimeoutMs)
	}
	if len(kafkaCfg.Properties) > 0 {
		cfg["kafka_properties"] = kafkaCfg.Properties
	}
	if kafkaCfg.Mock {
		cfg["kafka_mock"] = true
	}
}

func appendHTTPSecurityConfig(cfg map[string]any, script *dsl.Script) {
	httpCfg := script.Config.HTTP
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
	if httpCfg.ProxyMode != "" {
		cfg["proxy_mode"] = httpCfg.ProxyMode
	}
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
}
