package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

const maxAssignmentDataFiles = 64

// PrepareDistributedScript snapshots controller-local data files into bounded,
// content-addressed assignment assets. The returned script contains only
// opaque staged_asset references, so a generator can never reopen a path from
// the controller's filesystem namespace.
func PrepareDistributedScript(script *dsl.Script) ([]byte, string, []DataFile, error) {
	if script == nil {
		return nil, "", nil, fmt.Errorf("script is required")
	}
	content, err := json.Marshal(script)
	if err != nil {
		return nil, "", nil, fmt.Errorf("marshal script for staging: %w", err)
	}
	var staged dsl.Script
	if err := json.Unmarshal(content, &staged); err != nil {
		return nil, "", nil, fmt.Errorf("clone script for staging: %w", err)
	}

	files := make([]DataFile, 0)
	var totalBytes int64
	for index := range staged.DataSources {
		source := &staged.DataSources[index]
		if strings.TrimSpace(source.StagedAsset) != "" {
			return nil, "", nil, fmt.Errorf("data source %q contains an internal staged_asset reference", source.Name)
		}
		// Inline content is already self-contained and has the same precedence in
		// the local engine. Strip any dormant local reference before transport.
		if strings.TrimSpace(source.InlineData) != "" {
			source.Path = ""
			source.StorageRef = ""
			continue
		}

		path := strings.TrimSpace(source.Path)
		storageRef := strings.TrimSpace(source.StorageRef)
		if path == "" && storageRef == "" {
			continue
		}
		if path != "" && storageRef != "" {
			return nil, "", nil, fmt.Errorf("data source %q has both path and storage_ref", source.Name)
		}
		if len(files) >= maxAssignmentDataFiles {
			return nil, "", nil, fmt.Errorf("distributed data file count exceeds %d", maxAssignmentDataFiles)
		}

		var reader io.ReadCloser
		if path != "" {
			reader, err = fileasset.OpenPath(path)
		} else {
			reader, err = fileasset.OpenStorageRef(storageRef)
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("open data source %q for staging: %w", source.Name, err)
		}
		data, readErr := fileasset.ReadAllLimited(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return nil, "", nil, fmt.Errorf("read data source %q for staging: %w", source.Name, readErr)
		}
		if closeErr != nil {
			return nil, "", nil, fmt.Errorf("close data source %q after staging: %w", source.Name, closeErr)
		}
		if int64(len(data)) > defaultGeneratorScriptMaxBytes-totalBytes {
			return nil, "", nil, fmt.Errorf("distributed data files exceed %d byte aggregate limit", defaultGeneratorScriptMaxBytes)
		}
		totalBytes += int64(len(data))
		digest := sha256.Sum256(data)
		assetName := fmt.Sprintf("source-%03d-%s", index, hex.EncodeToString(digest[:8]))
		files = append(files, DataFile{
			Name:    assetName,
			Content: data,
			SHA256:  hex.EncodeToString(digest[:]),
		})
		source.StagedAsset = assetName
		source.Path = ""
		source.StorageRef = ""
	}

	content, err = json.Marshal(&staged)
	if err != nil {
		return nil, "", nil, fmt.Errorf("marshal staged script: %w", err)
	}
	digest := sha256.Sum256(content)
	return content, hex.EncodeToString(digest[:]), files, nil
}

func assignmentDataFiles(assignment *pb.WorkAssignment, script *dsl.Script, aggregateLimit int64) (map[string][]byte, error) {
	if assignment == nil || script == nil {
		return nil, fmt.Errorf("assignment and script are required")
	}
	files := assignment.GetDataFiles()
	if len(files) > maxAssignmentDataFiles {
		return nil, fmt.Errorf("assignment has %d data files; maximum is %d", len(files), maxAssignmentDataFiles)
	}
	if aggregateLimit <= 0 {
		return nil, fmt.Errorf("data file aggregate limit must be positive")
	}

	assets := make(map[string][]byte, len(files))
	var totalBytes int64
	for index, file := range files {
		if file == nil {
			return nil, fmt.Errorf("data_files[%d] is nil", index)
		}
		name := strings.TrimSpace(file.GetName())
		if !validStagedAssetName(name) || name != file.GetName() {
			return nil, fmt.Errorf("data_files[%d].name must be an unpadded opaque name of at most 256 bytes", index)
		}
		if _, duplicate := assets[name]; duplicate {
			return nil, fmt.Errorf("duplicate data file %q", name)
		}
		if int64(len(file.GetContent())) > fileasset.MaxBytes {
			return nil, fmt.Errorf("data file %q exceeds %d byte limit", name, fileasset.MaxBytes)
		}
		if int64(len(file.GetContent())) > aggregateLimit-totalBytes {
			return nil, fmt.Errorf("assignment data files exceed %d byte aggregate limit", aggregateLimit)
		}
		totalBytes += int64(len(file.GetContent()))
		digest := sha256.Sum256(file.GetContent())
		expected, err := hex.DecodeString(strings.TrimSpace(file.GetSha256()))
		if err != nil || len(expected) != sha256.Size || !equalBytes(expected, digest[:]) {
			return nil, fmt.Errorf("data file %q sha256 does not match content", name)
		}
		assets[name] = append([]byte(nil), file.GetContent()...)
	}

	referenced := make(map[string]struct{}, len(assets))
	for index, source := range script.DataSources {
		stagedName := strings.TrimSpace(source.StagedAsset)
		if stagedName != "" {
			if !validStagedAssetName(stagedName) || stagedName != source.StagedAsset {
				return nil, fmt.Errorf("data_sources[%d] has invalid staged_asset", index)
			}
			if strings.TrimSpace(source.InlineData) != "" || strings.TrimSpace(source.Path) != "" || strings.TrimSpace(source.StorageRef) != "" {
				return nil, fmt.Errorf("data source %q combines staged_asset with local or inline content", source.Name)
			}
			if _, ok := assets[stagedName]; !ok {
				return nil, fmt.Errorf("data source %q references missing staged asset %q", source.Name, stagedName)
			}
			referenced[stagedName] = struct{}{}
			continue
		}
		// An inline source is transport-safe. Any path/storage reference is not:
		// accepting even a currently dormant one would leave execution semantics
		// dependent on precedence and the generator filesystem.
		if strings.TrimSpace(source.Path) != "" || strings.TrimSpace(source.StorageRef) != "" {
			return nil, fmt.Errorf("data source %q contains an unstaged local file reference", source.Name)
		}
	}
	for name := range assets {
		if _, ok := referenced[name]; !ok {
			return nil, fmt.Errorf("data file %q is not referenced by the staged script", name)
		}
	}
	return assets, nil
}

func validStagedAssetName(name string) bool {
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
