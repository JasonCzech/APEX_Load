package enterprisearchive

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type javaSurface string

const (
	javaSurfaceLR       javaSurface = "lr"
	javaSurfaceHTTP     javaSurface = "http"
	javaSurfaceJMS      javaSurface = "jms"
	javaSurfaceDatabase javaSurface = "database"
	javaSurfaceSOAP     javaSurface = "soap"
	javaSurfaceReview   javaSurface = "review"
)

type JavaProfile struct {
	Surfaces        map[javaSurface]bool
	Primary         javaSurface
	JMSProvider     string
	JMSProtocol     string
	DestinationType string
	ReviewFamilies  []string
}

func classifyJavaArchive(program javaProgram, projectConfig *ProjectConfig, files map[string]string) JavaProfile {
	text := strings.ToLower(joinArchiveText(files))
	for _, class := range program.Classes {
		for _, imp := range class.Imports {
			text += "\nimport " + strings.ToLower(imp)
		}
	}
	profile := JavaProfile{
		Surfaces:        map[javaSurface]bool{},
		JMSProvider:     "solace",
		JMSProtocol:     "jms-solace",
		DestinationType: "queue",
	}
	if strings.Contains(text, "lrapi") || strings.Contains(text, "lr.") {
		profile.Surfaces[javaSurfaceLR] = true
	}
	if javaLooksLikeHTTP(text) {
		profile.Surfaces[javaSurfaceHTTP] = true
	}
	if javaLooksLikeJMS(text) {
		profile.Surfaces[javaSurfaceJMS] = true
		profile.JMSProvider, profile.JMSProtocol = inferJavaJMSProvider(text)
		profile.DestinationType = inferJavaJMSDestinationType(text)
	}
	if javaLooksLikeDatabase(text) {
		profile.Surfaces[javaSurfaceDatabase] = true
	}
	if javaLooksLikeSOAP(text) {
		profile.Surfaces[javaSurfaceSOAP] = true
	}
	profile.ReviewFamilies = inferJavaReviewFamilies(text)
	if len(program.Unparsed) > 0 {
		profile.ReviewFamilies = appendUnique(profile.ReviewFamilies, "unparsed_java_region")
	}
	if len(profile.ReviewFamilies) > 0 {
		profile.Surfaces[javaSurfaceReview] = true
	}

	switch {
	case profile.Surfaces[javaSurfaceHTTP]:
		profile.Primary = javaSurfaceHTTP
	case profile.Surfaces[javaSurfaceJMS]:
		profile.Primary = javaSurfaceJMS
	case profile.Surfaces[javaSurfaceDatabase]:
		profile.Primary = javaSurfaceDatabase
	case profile.Surfaces[javaSurfaceSOAP]:
		profile.Primary = javaSurfaceSOAP
	case profile.Surfaces[javaSurfaceReview]:
		profile.Primary = javaSurfaceReview
	default:
		profile.Primary = javaSurfaceLR
	}
	if projectConfig != nil {
		if profileForProject, _, ok := profileForProjectConfig(projectConfig); ok && profileForProject.Canonical == "java_jms_solace" {
			profile.Surfaces[javaSurfaceJMS] = true
			profile.JMSProvider = "solace"
			profile.JMSProtocol = "jms-solace"
			profile.Primary = javaSurfaceJMS
		}
	}
	return profile
}

func javaLooksLikeHTTP(text string) bool {
	return strings.Contains(text, "web.") ||
		strings.Contains(text, "httpurlconnection") ||
		strings.Contains(text, "java.net.http") ||
		strings.Contains(text, "httpclient") ||
		strings.Contains(text, "org.apache.http") ||
		strings.Contains(text, ".openconnection(")
}

func javaLooksLikeJMS(text string) bool {
	return strings.Contains(text, "javax.jms") ||
		strings.Contains(text, "jakarta.jms") ||
		strings.Contains(text, "messageproducer") ||
		strings.Contains(text, "textmessage") ||
		strings.Contains(text, ".createqueue(") ||
		strings.Contains(text, ".createtopic(") ||
		strings.Contains(text, ".send(") && strings.Contains(text, "jms")
}

func javaLooksLikeDatabase(text string) bool {
	return strings.Contains(text, "java.sql") ||
		strings.Contains(text, "drivermanager") ||
		strings.Contains(text, "preparedstatement") ||
		strings.Contains(text, "executestatement") ||
		strings.Contains(text, "executequery(") ||
		strings.Contains(text, "executeupdate(")
}

func javaLooksLikeSOAP(text string) bool {
	return strings.Contains(text, "javax.xml.ws") ||
		strings.Contains(text, "jakarta.xml.ws") ||
		strings.Contains(text, "soapmessage") ||
		strings.Contains(text, "soapconnection") ||
		strings.Contains(text, "org.apache.axis") ||
		strings.Contains(text, "org.apache.cxf")
}

func inferJavaJMSProvider(text string) (string, string) {
	switch {
	case strings.Contains(text, "servicebus") || strings.Contains(text, "azure.servicebus"):
		return "servicebus", "jms-servicebus"
	case strings.Contains(text, "com.ibm.mq") || strings.Contains(text, "wmq") || strings.Contains(text, "ibm.mq"):
		return "ibmmq", "jms-ibmmq"
	default:
		return "solace", "jms-solace"
	}
}

func inferJavaJMSDestinationType(text string) string {
	if strings.Contains(text, ".createtopic(") || strings.Contains(text, "topicpublisher") || strings.Contains(text, "topic ") || strings.Contains(text, "javax.jms.topic") {
		return "topic"
	}
	return "queue"
}

func inferJavaReviewFamilies(text string) []string {
	families := make([]string, 0)
	add := func(name string) {
		for _, existing := range families {
			if existing == name {
				return
			}
		}
		families = append(families, name)
	}
	if strings.Contains(text, "javax.ejb") || strings.Contains(text, "jakarta.ejb") {
		add("ejb_review")
	}
	if strings.Contains(text, "org.omg.corba") || strings.Contains(text, "corba.orb") {
		add("corba_review")
	}
	if strings.Contains(text, "naming.lookup") || strings.Contains(text, "initialcontext.lookup") || strings.Contains(text, "javax.rmi") {
		add("rmi_java_review")
	}
	if strings.Contains(text, "sapgui") || strings.Contains(text, "com.sap") {
		add("sapgui_java_review")
	}
	if strings.Contains(text, "jacada") {
		add("jacada_java_review")
	}
	if strings.Contains(text, "oracle.apps") || strings.Contains(text, "oracle.forms") || strings.Contains(text, "oracle.nca") {
		add("oracle_nca_java_review")
	}
	if strings.Contains(text, "tuxedo") || strings.Contains(text, "weblogic.wtc") {
		add("tuxedo_java_review")
	}
	sort.Strings(families)
	return families
}

func newJavaScriptForSurface(opts ConvertOptions, profile JavaProfile, surface javaSurface) *dsl.Script {
	name := convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_Vuser")
	if len(profile.Surfaces) > 1 {
		name = name + "_" + string(surface)
	}
	script := &dsl.Script{
		Name:          sanitizeName(name),
		Version:       "1.0.0",
		GeneratedBy:   "apexload-enterprise-script-archive-java-importer",
		GeneratedFrom: "enterprise_script_archive",
		Actions:       make([]dsl.Action, 0),
	}
	switch surface {
	case javaSurfaceHTTP, javaSurfaceSOAP:
		script.Protocol = "http"
		script.Config.HTTP = dsl.HTTPConfig{}
	case javaSurfaceDatabase:
		script.Protocol = "database"
		script.Config.Database = dsl.DatabaseConfig{
			Driver:         "postgres",
			DSN:            "{{secret.database_dsn}}",
			QueryTimeoutMs: 30000,
			MaxRows:        100,
		}
	case javaSurfaceJMS:
		script.Protocol = profile.JMSProtocol
		script.Config.JMS = dsl.JMSConfig{
			Provider:        profile.JMSProvider,
			DestinationType: profile.DestinationType,
			TimeoutMs:       5000,
			Mock:            true,
		}
	default:
		script.Protocol = "java_vuser"
	}
	return script
}

func sortedJavaSourceFiles(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		if strings.HasSuffix(strings.ToLower(filepath.Base(name)), ".java") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
