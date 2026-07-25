package enterprisearchive

import "testing"

func TestParseParameterFileResolvesGeneratedColumnToMatchingHeader(t *testing.T) {
	parsed := ParseParameterFile("NEXT31.prm", `[parameter:ssn]
ColumnName="Col 1"
Delimiter=","
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="ssn"
SelectNextRow="Sequential"
StartRow="1"
Table="SPI_Info.dat"
Type="Table"
`, map[string]string{
		"SPI_Info.dat": "birth_date,ssn,dl_number\nsample-date,sample-ssn,sample-dl\n",
	})

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	if len(parsed.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one parsed parameter", parsed.Parameters)
	}
	param := parsed.Parameters[0]
	if param.Name != "ssn" || param.DataSource != "spi_info" || param.Column != "ssn" {
		t.Fatalf("parameter = %#v, want ssn bound to SPI_Info.dat ssn header", param)
	}
	if len(parsed.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one parsed data source", parsed.DataSources)
	}
	source := parsed.DataSources[0]
	if source.Name != "spi_info" || source.Path != "" || source.FileName != "SPI_Info.dat" || !source.FirstRowHeader || len(source.Columns) != 0 {
		t.Fatalf("data source = %#v, want first-row-header SPI_Info.dat source", source)
	}
}

func TestParseParameterFileKeepsGeneratedColumnWithoutMatchingHeader(t *testing.T) {
	parsed := ParseParameterFile("Users.prm", `[parameter:account]
ColumnName="Col 2"
Delimiter=","
GenerateNewVal="EachIteration"
ParamName="account"
SelectNextRow="Sequential"
Table="Users.dat"
Type="Table"
`, map[string]string{
		"Users.dat": "first_name,last_name\nAda,Lovelace\n",
	})

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	if len(parsed.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one parsed parameter", parsed.Parameters)
	}
	param := parsed.Parameters[0]
	if param.Column != "Col 2" {
		t.Fatalf("parameter = %#v, want generated column retained when no matching header exists", param)
	}
	if len(parsed.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one parsed data source", parsed.DataSources)
	}
	source := parsed.DataSources[0]
	if source.FirstRowHeader || len(source.Columns) != 2 || source.Columns[1] != "Col 2" {
		t.Fatalf("data source = %#v, want generated-column data source fallback", source)
	}
}
