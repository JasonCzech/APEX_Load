package analysis

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/xitongsys/parquet-go/parquet"
)

const maxParquetFooterBytes = 32 << 20
const maxParquetSchemaElements = 4096
const maxParquetColumns = 1024
const maxParquetNesting = 16

// ParquetSchemaPreview inspects file metadata without reading data pages.
// Callers must supply a file path from an already authorized, controlled import
// staging area; this function never treats a client path as authorization.
type ParquetSchemaPreview struct {
	Rows     int64           `json:"rows"`
	Columns  []ParquetColumn `json:"columns"`
	FileSize int64           `json:"file_size"`
}

type ParquetColumn struct {
	Path           string               `json:"path"`
	PhysicalType   string               `json:"physical_type"`
	ConvertedType  string               `json:"converted_type,omitempty"`
	Repetition     string               `json:"repetition,omitempty"`
	PathComponents []string             `json:"path_components"`
	LogicalType    *parquet.LogicalType `json:"logical_type,omitempty"`
	Precision      *int32               `json:"precision,omitempty"`
	Scale          *int32               `json:"scale,omitempty"`
	TypeLength     *int32               `json:"type_length,omitempty"`
	Repeated       bool                 `json:"repeated"`
	Nullable       bool                 `json:"nullable"`
}

func InspectParquet(path string) (ParquetSchemaPreview, error) {
	file, err := os.Open(path)
	if err != nil {
		return ParquetSchemaPreview{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ParquetSchemaPreview{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 12 {
		return ParquetSchemaPreview{}, errors.New("Parquet source must be a regular file of at least 12 bytes")
	}
	var magic [4]byte
	if _, err := file.ReadAt(magic[:], 0); err != nil || !bytes.Equal(magic[:], []byte("PAR1")) {
		return ParquetSchemaPreview{}, errors.New("invalid Parquet header")
	}
	var footer [8]byte
	if _, err := file.ReadAt(footer[:], info.Size()-8); err != nil || !bytes.Equal(footer[4:], []byte("PAR1")) {
		return ParquetSchemaPreview{}, errors.New("invalid Parquet trailer")
	}
	footerSize := int64(binary.LittleEndian.Uint32(footer[:4]))
	if footerSize <= 0 || footerSize > maxParquetFooterBytes || footerSize > info.Size()-12 {
		return ParquetSchemaPreview{}, fmt.Errorf("Parquet footer exceeds bounds")
	}
	// Decode only the validated footer bytes from this descriptor. Reopening the
	// path could inspect a different file and bypass the size check.
	data := make([]byte, int(footerSize))
	if _, err := file.ReadAt(data, info.Size()-8-footerSize); err != nil {
		return ParquetSchemaPreview{}, err
	}
	transport := &thrift.TMemoryBuffer{Buffer: bytes.NewBuffer(data)}
	protocol := &boundedMetadataProtocol{TProtocol: thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{MaxMessageSize: maxParquetFooterBytes}), remaining: maxParquetMetadataItems}
	footerMeta := parquet.NewFileMetaData()
	if err := footerMeta.Read(context.Background(), protocol); err != nil {
		return ParquetSchemaPreview{}, fmt.Errorf("read Parquet footer: %w", err)
	}
	if transport.Len() != 0 {
		return ParquetSchemaPreview{}, errors.New("Parquet footer has trailing bytes")
	}
	if footerMeta == nil || footerMeta.NumRows < 0 || len(footerMeta.Schema) == 0 || len(footerMeta.Schema) > maxParquetSchemaElements {
		return ParquetSchemaPreview{}, errors.New("invalid or excessive Parquet schema")
	}
	columns, err := flattenParquetSchema(footerMeta.Schema)
	if err != nil {
		return ParquetSchemaPreview{}, err
	}
	return ParquetSchemaPreview{Rows: footerMeta.NumRows, Columns: columns, FileSize: info.Size()}, nil
}

func flattenParquetSchema(elements []*parquet.SchemaElement) ([]ParquetColumn, error) {
	if len(elements) > maxParquetSchemaElements || len(elements) == 0 || elements[0] == nil || elements[0].GetNumChildren() <= 0 {
		return nil, errors.New("Parquet root must contain columns")
	}
	index := 0
	columns := make([]ParquetColumn, 0)
	seen := make(map[string]bool)
	var visit func([]string, int, bool, bool) error
	visit = func(path []string, depth int, repeated, nullable bool) error {
		if depth > maxParquetNesting || index >= len(elements) {
			return errors.New("Parquet schema nesting or element count exceeds bounds")
		}
		element := elements[index]
		index++
		if element == nil || element.Name == "" || len(element.Name) > 256 || strings.ContainsAny(element.Name, "\x00\n\r") {
			return errors.New("invalid Parquet column name")
		}
		path = append(path, element.Name)
		encodedPath, _ := json.Marshal(path)
		if seen[string(encodedPath)] {
			return errors.New("duplicate Parquet field path")
		}
		seen[string(encodedPath)] = true
		if depth > 0 && element.RepetitionType == nil {
			return errors.New("Parquet non-root field requires a repetition type")
		}
		if element.LogicalType != nil && element.LogicalType.CountSetFieldsLogicalType() != 1 {
			return errors.New("unsupported or ambiguous Parquet logical type")
		}
		if element.RepetitionType != nil {
			if *element.RepetitionType < parquet.FieldRepetitionType_REQUIRED || *element.RepetitionType > parquet.FieldRepetitionType_REPEATED {
				return errors.New("invalid Parquet repetition type")
			}
			repeated = repeated || *element.RepetitionType == parquet.FieldRepetitionType_REPEATED
			nullable = nullable || *element.RepetitionType == parquet.FieldRepetitionType_OPTIONAL
		}
		children := int(element.GetNumChildren())
		if children < 0 || children > maxParquetSchemaElements {
			return errors.New("invalid Parquet child count")
		}
		if children > 0 {
			if element.Type != nil {
				return errors.New("Parquet group cannot have a physical type")
			}
			for i := 0; i < children; i++ {
				if err := visit(path, depth+1, repeated, nullable); err != nil {
					return err
				}
			}
			return nil
		}
		if element.Type == nil || len(columns) >= maxParquetColumns {
			return errors.New("Parquet leaf type missing or column limit exceeded")
		}
		if *element.Type < parquet.Type_BOOLEAN || *element.Type > parquet.Type_FIXED_LEN_BYTE_ARRAY {
			return errors.New("invalid Parquet physical type")
		}
		parts := append([]string(nil), path[1:]...)
		escaped := make([]string, len(parts))
		for i, part := range parts {
			escaped[i] = strings.NewReplacer(`\`, `\\`, `.`, `\.`).Replace(part)
		}
		column := ParquetColumn{Path: strings.Join(escaped, "."), PathComponents: parts, PhysicalType: element.Type.String(),
			LogicalType: element.LogicalType, Precision: element.Precision, Scale: element.Scale, TypeLength: element.TypeLength,
			Repeated: repeated, Nullable: nullable}
		if element.ConvertedType != nil {
			column.ConvertedType = element.ConvertedType.String()
		}
		if element.RepetitionType != nil {
			column.Repetition = element.RepetitionType.String()
		}
		columns = append(columns, column)
		return nil
	}
	if err := visit(nil, 0, false, false); err != nil {
		return nil, err
	}
	if index != len(elements) || len(columns) == 0 {
		return nil, errors.New("Parquet schema has trailing or missing columns")
	}
	return columns, nil
}
