package trafficprofile

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/xuri/excelize/v2"
)

const (
	xlsxUnzipSizeLimit    = 100 << 20
	xlsxUnzipXMLSizeLimit = 32 << 20
)

func ParseXLSX(data []byte) ([]Sheet, error) {
	book, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    xlsxUnzipSizeLimit,
		UnzipXMLSizeLimit: xlsxUnzipXMLSizeLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("open XLSX: %w", err)
	}
	defer book.Close()
	names := book.GetSheetList()
	if len(names) == 0 {
		return nil, errors.New("XLSX contains no worksheets")
	}
	sheets := make([]Sheet, 0, len(names))
	for _, name := range names {
		rows, err := book.GetRows(name)
		if err != nil {
			return nil, fmt.Errorf("read worksheet %q: %w", name, err)
		}
		if len(rows) == 0 {
			continue
		}
		columns := normalizedColumns(rows[0])
		if err := uniqueColumns(columns); err != nil {
			return nil, fmt.Errorf("worksheet %q: %w", name, err)
		}
		sheet := newSheet(name, columns, rows[1:])
		sheets = append(sheets, *sheet)
	}
	if len(sheets) == 0 {
		return nil, errors.New("XLSX contains no non-empty worksheets")
	}
	return sheets, nil
}
