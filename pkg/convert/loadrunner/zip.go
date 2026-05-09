package loadrunner

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// ConvertZip processes a ZIP archive containing a LoadRunner script folder
// and returns the converted Stormrunner DSL script.
func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to open zip archive: %w", err)
	}

	// Read all files from the zip
	files := make(map[string]string)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		// Normalize path: strip leading directory prefix if all files share one
		name := filepath.Base(f.Name)
		dirName := filepath.Dir(f.Name)
		if dirName != "." {
			// Keep relative path from root dir
			name = strings.TrimPrefix(f.Name, strings.SplitN(f.Name, "/", 2)[0]+"/")
			if name == "" {
				name = filepath.Base(f.Name)
			}
		}
		files[name] = string(data)
	}

	// Find and parse .usr file for project config
	var projectConfig *ProjectConfig
	for name, content := range files {
		if strings.HasSuffix(strings.ToLower(name), ".usr") {
			cfg, err := ParseUSRFile(content)
			if err == nil {
				projectConfig = cfg
				// Use .usr filename (without extension) as script name if not set
				if opts.ScriptName == "" {
					opts.ScriptName = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
				}
			}
			break
		}
	}

	// Determine which .c files to parse
	actionFileNames := []string{"Action.c"}
	if projectConfig != nil && len(projectConfig.ActionFiles) > 0 {
		actionFileNames = projectConfig.ActionFiles
	}

	// Also look for vuser_init.c and vuser_end.c
	initFile := "vuser_init.c"
	endFile := "vuser_end.c"

	// Parse action files
	var parsedActions []*ParsedAction

	// Parse init if exists
	if content, ok := findFile(files, initFile); ok {
		parsed, err := ParseActionFile(initFile, content)
		if err == nil && len(parsed.Functions) > 0 {
			parsedActions = append(parsedActions, parsed)
		}
	}

	// Parse main action files
	for _, actionFile := range actionFileNames {
		content, ok := findFile(files, actionFile)
		if !ok {
			// Try case-insensitive
			for name, c := range files {
				if strings.EqualFold(filepath.Base(name), actionFile) {
					content = c
					ok = true
					break
				}
			}
		}
		if ok {
			parsed, err := ParseActionFile(actionFile, content)
			if err == nil {
				parsedActions = append(parsedActions, parsed)
			}
		}
	}

	// Parse end if exists
	if content, ok := findFile(files, endFile); ok {
		parsed, err := ParseActionFile(endFile, content)
		if err == nil && len(parsed.Functions) > 0 {
			parsedActions = append(parsedActions, parsed)
		}
	}

	if len(parsedActions) == 0 {
		return nil, fmt.Errorf("no parseable .c action files found in the ZIP archive")
	}

	// Run the converter
	return Convert(parsedActions, opts)
}

// findFile looks up a file in the map by base name, handling path variations.
func findFile(files map[string]string, name string) (string, bool) {
	// Direct match
	if content, ok := files[name]; ok {
		return content, true
	}
	// Try base name match
	baseName := filepath.Base(name)
	for key, content := range files {
		if filepath.Base(key) == baseName {
			return content, true
		}
	}
	return "", false
}
