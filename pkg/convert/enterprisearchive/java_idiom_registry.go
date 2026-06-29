package enterprisearchive

type JavaIdiomMapper interface {
	Name() string
	Map(*javaMapperContext) (bool, error)
}

type javaMapperContext struct {
	result    *ConvertResult
	state     *javaImportState
	files     map[string]string
	phase     string
	statement javaStatement
	text      string
}

type javaIdiomMapperFunc struct {
	name string
	fn   func(*javaMapperContext) (bool, error)
}

func (m javaIdiomMapperFunc) Name() string { return m.name }

func (m javaIdiomMapperFunc) Map(ctx *javaMapperContext) (bool, error) {
	return m.fn(ctx)
}

var javaStatementMapperRegistry = []JavaIdiomMapper{
	javaIdiomMapperFunc{name: "java_http_owned_by_http_lane", fn: mapJavaHTTPOwnedStatement},
	javaIdiomMapperFunc{name: "java_database_owned_by_database_lane", fn: mapJavaDatabaseOwnedStatement},
	javaIdiomMapperFunc{name: "java_symbolic_state", fn: mapJavaSymbolicStateStatement},
	javaIdiomMapperFunc{name: "java_inert_statement", fn: mapJavaInertStatement},
	javaIdiomMapperFunc{name: "java_jndi_config", fn: mapJavaJNDIConfigStatement},
	javaIdiomMapperFunc{name: "java_message_property", fn: mapJavaMessagePropertyStatement},
	javaIdiomMapperFunc{name: "java_lr_owned_by_executable_lane", fn: mapJavaLROwnedByExecutableLane},
	javaIdiomMapperFunc{name: "java_lr_api", fn: mapJavaLRStatement},
	javaIdiomMapperFunc{name: "java_jms_api", fn: mapJavaJMSStatement},
	javaIdiomMapperFunc{name: "java_named_review", fn: mapJavaNamedReviewStatement},
	javaIdiomMapperFunc{name: "java_source_review_fallback", fn: mapJavaFallbackReviewStatement},
}

func mapJavaHTTPOwnedStatement(ctx *javaMapperContext) (bool, error) {
	if !ctx.state.skipHTTP {
		return false, nil
	}
	if _, ok := javaWebToParsedFunction(ctx.statement, ctx.phase); ok {
		return true, nil
	}
	if _, ok := javaHTTPClientToParsedFunction(ctx.statement, ctx.phase); ok {
		return true, nil
	}
	if javaHTTPBuilderStatementLooksOwned(ctx.text) {
		return true, nil
	}
	if javaURLConnectionStatementLooksOwned(ctx.text) {
		return true, nil
	}
	return false, nil
}

func mapJavaDatabaseOwnedStatement(ctx *javaMapperContext) (bool, error) {
	return ctx.state.skipDatabase && javaStatementLooksLikeDatabase(ctx.text), nil
}

func mapJavaSymbolicStateStatement(ctx *javaMapperContext) (bool, error) {
	if ctx.state.env.assignDestination(ctx.text, ctx.files) ||
		ctx.state.env.assignLookupDestination(ctx.text, ctx.files) ||
		ctx.state.env.assignProducer(ctx.text, ctx.files) ||
		ctx.state.env.assignMessage(ctx.text, ctx.files) ||
		ctx.state.env.assignString(ctx.text, ctx.files) {
		appendJavaMapping(ctx.result, ctx.statement, "", "", "state", "converted", "Mapped Java symbolic state used by downstream JMS actions.", SourceFunctionSupportExecutable)
		return true, nil
	}
	return false, nil
}

func mapJavaInertStatement(ctx *javaMapperContext) (bool, error) {
	if !isJavaInertStatement(ctx.text) {
		return false, nil
	}
	appendJavaMapping(ctx.result, ctx.statement, "", statementName(ctx.statement), "noop", "converted", "Java statement has no executable replay effect and was recorded as an explicit no-op disposition.", SourceFunctionSupportNativeNoop)
	return true, nil
}

func mapJavaJNDIConfigStatement(ctx *javaMapperContext) (bool, error) {
	return convertJavaEnvPut(ctx.result, ctx.state, ctx.files, ctx.statement), nil
}

func mapJavaMessagePropertyStatement(ctx *javaMapperContext) (bool, error) {
	return convertJavaMessageProperty(ctx.result, ctx.state, ctx.files, ctx.statement), nil
}

// mapJavaLROwnedByExecutableLane suppresses lr.* utility statements in the JMS
// statement lane when an executable HTTP/JDBC lane is present: that lane already
// converts the lr.* call (via the shared C registry through Convert/ConvertDatabase)
// and records its disposition, so re-emitting here would double-count
// transactions/think-times and duplicate the structure across scripts.
func mapJavaLROwnedByExecutableLane(ctx *javaMapperContext) (bool, error) {
	if !ctx.state.skipHTTP && !ctx.state.skipDatabase {
		return false, nil
	}
	if fn, ok := javaLRToParsedFunction(ctx.statement, ctx.phase); ok && javaLRFunctionAllowedInExecutableLane(fn.Name) {
		return true, nil
	}
	return false, nil
}

func mapJavaLRStatement(ctx *javaMapperContext) (bool, error) {
	if fn, ok := javaLRToParsedFunction(ctx.statement, ctx.phase); ok {
		routeJavaLRFunction(ctx.result, ctx.state, ctx.phase, ctx.statement, fn)
		return true, nil
	}
	return false, nil
}

func mapJavaJMSStatement(ctx *javaMapperContext) (bool, error) {
	// Only treat .send()/.receive()/.commit()/.acknowledge() idioms as JMS when the
	// archive actually shows JMS evidence; otherwise a bare receiver.send(...) in a
	// non-JMS Java Vuser would be fabricated into a jms_publish.
	if !ctx.state.profile.Surfaces[javaSurfaceJMS] {
		return false, nil
	}
	if action, ok := convertJavaJMSAction(ctx.state, ctx.files, ctx.statement); ok {
		if unresolved, _ := action.Metadata["body_unresolved"].(bool); unresolved {
			// An unresolvable body would replay as an empty broker message; preserve
			// it for review instead of shipping it as executable.
			appendJavaReview(ctx.result, ctx.state, ctx.statement, ctx.phase, "unresolved_jms_body", ctx.phase == "action")
			return true, nil
		}
		applyJavaTransactionMetadata(&action, ctx.state)
		appendJavaAction(ctx.result, ctx.statement, ctx.phase, action, SourceFunctionSupportExecutable, "Mapped Java JMS API idiom to native JMS action.")
		ctx.state.jmsActions++
		return true, nil
	}
	return false, nil
}

func mapJavaNamedReviewStatement(ctx *javaMapperContext) (bool, error) {
	if reason := javaNamedReviewReason(ctx.text); reason != "" {
		appendJavaReview(ctx.result, ctx.state, ctx.statement, ctx.phase, reason, ctx.phase == "action")
		return true, nil
	}
	return false, nil
}

func mapJavaFallbackReviewStatement(ctx *javaMapperContext) (bool, error) {
	appendJavaReview(ctx.result, ctx.state, ctx.statement, ctx.phase, "unmapped_java_statement", ctx.phase == "action")
	return true, nil
}
