package enterprisearchive

// This file keeps focused coverage of the static Java idiom analyzers after
// production imports stopped emitting protocol-specific lanes. The helper is
// deliberately test-only: ConvertJava always returns one combined java_vuser.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertZipJavaNativeAnalysisForTest(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, err
	}
	files := make(map[string]string)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		r, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		name := filepath.Base(file.Name)
		if strings.Contains(file.Name, "/") {
			name = strings.SplitN(file.Name, "/", 2)[1]
		}
		files[name] = string(data)
	}
	var projectConfig *ProjectConfig
	for name, content := range files {
		if !strings.HasSuffix(strings.ToLower(name), ".usr") {
			continue
		}
		if cfg, parseErr := ParseUSRFile(content); parseErr == nil {
			projectConfig = cfg
			if opts.ScriptName == "" {
				opts.ScriptName = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
			}
		}
		break
	}
	detected := DetectProtocol(files, projectConfig, nil)
	return convertJavaNativeAnalysisForTest(files, opts, detected, projectConfig)
}

func convertJavaNativeAnalysisForTest(files map[string]string, opts ConvertOptions, detected DetectedProtocol, projectConfig *ProjectConfig) (*ConvertResult, error) {
	program, parseWarnings := parseJavaProgram(files)
	profile := classifyJavaArchive(program, projectConfig, files)
	opts.UseMaskedBodySecretReferences = true
	if len(program.Methods) == 0 {
		return nil, fmt.Errorf("no parseable Java lifecycle methods")
	}

	var laneResults []*ConvertResult
	httpLaneEmitted := false
	databaseLaneEmitted := false
	if profile.Surfaces[javaSurfaceHTTP] {
		if parsed := javaHTTPParsedActions(program); len(flattenParsedFunctions(parsed)) > 0 {
			lane, err := Convert(parsed, opts)
			if err != nil {
				return nil, err
			}
			if lane != nil && lane.Script != nil {
				laneDetected := detected
				laneDetected.CanonicalSourceProtocol = "java_over_http"
				laneDetected.APEXLoadProtocol = "http"
				laneDetected.ImportMode = ImportModeExecutable
				laneDetected.DisplayName = "Java Vuser HTTP"
				lane.DetectedProtocol = &laneDetected
				lane.Script.Name = convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_HTTP")
				lane.Script.GeneratedBy = "apexload-enterprise-script-archive-java-importer"
				lane.Script.GeneratedFrom = "enterprise_script_archive"
				javaEnsureHeaderLists(lane.Script)
				laneResults = append(laneResults, lane)
				httpLaneEmitted = true
			}
		}
	}
	if profile.Surfaces[javaSurfaceDatabase] {
		if parsed := javaDatabaseParsedActions(program, files); len(flattenParsedFunctions(parsed)) > 0 {
			laneDetected := detected
			laneDetected.CanonicalSourceProtocol = "java_jdbc"
			laneDetected.APEXLoadProtocol = "database"
			laneDetected.ImportMode = ImportModeExecutable
			laneDetected.DisplayName = "Java Vuser JDBC"
			lane, err := ConvertDatabase(parsed, opts, laneDetected)
			if err != nil {
				return nil, err
			}
			if lane != nil && lane.Script != nil {
				lane.Script.Name = convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_JDBC")
				if len(laneResults) > 0 {
					lane.Script.Name += "_database"
				}
				if profile.DatabaseDriver != "" {
					lane.Script.Config.Database.Driver = profile.DatabaseDriver
				}
				laneResults = append(laneResults, lane)
				databaseLaneEmitted = true
			}
		}
	}
	if profile.Surfaces[javaSurfaceSOAP] {
		if parsed := javaSOAPParsedActions(program, files); len(flattenParsedFunctions(parsed)) > 0 {
			lane, err := Convert(parsed, opts)
			if err != nil {
				return nil, err
			}
			if lane != nil && lane.Script != nil {
				laneDetected := detected
				laneDetected.CanonicalSourceProtocol = "java_soap"
				laneDetected.APEXLoadProtocol = lane.Script.Protocol
				laneDetected.ImportMode = ImportModeExecutable
				laneDetected.DisplayName = "Java Vuser SOAP"
				lane.DetectedProtocol = &laneDetected
				lane.Script.Name = convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_SOAP")
				if len(laneResults) > 0 {
					lane.Script.Name += "_soap"
				}
				lane.Script.GeneratedBy = "apexload-enterprise-script-archive-java-importer"
				lane.Script.GeneratedFrom = "enterprise_script_archive"
				javaEnsureHeaderLists(lane.Script)
				laneResults = append(laneResults, lane)
			}
		}
	}
	if profile.Surfaces[javaSurfaceJMS] || len(laneResults) == 0 || profile.Surfaces[javaSurfaceReview] {
		lane, err := convertJavaStatementLane(files, opts, detected, program, parseWarnings, profile, len(laneResults) > 0, httpLaneEmitted, databaseLaneEmitted)
		if err != nil {
			return nil, err
		}
		if lane != nil && lane.Script != nil && (len(lane.Script.Actions)+len(lane.Script.Init)+len(lane.Script.Teardown) > 0 || len(laneResults) == 0) {
			laneResults = append(laneResults, lane)
		}
	}
	if len(laneResults) == 0 {
		result := &ConvertResult{DetectedProtocol: &detected, Script: newJavaScriptForSurface(opts, profile, profile.Primary)}
		result.Script.Actions = append(result.Script.Actions, javaSourceReviewAction(javaStatement{Text: "No executable Java actions were recognized.", StartLine: 1, EndLine: 1}, "action", "no_java_actions"))
		result.Scripts = []*dsl.Script{result.Script}
		backfillJavaConservation(result, program)
		return result, nil
	}
	result := mergeJavaLaneResults(laneResults, detected, files, profile)
	backfillJavaConservation(result, program)
	return result, nil
}
