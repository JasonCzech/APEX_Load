package sharedvalue

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// A source variable the collection never supplied can take one value for
// every virtual user (Apply), or a binding: a data pool column read per
// virtual user, or a value generated each iteration.
const (
	BindingData      = "data"
	BindingGenerated = "generated"

	GeneratorUUID      = "uuid"
	GeneratorTimestamp = "timestamp"
	GeneratorDigits    = "digits"

	maxGeneratedDigits = 64
)

// ErrBindingUnsupported marks a binding the variable's uses cannot take, such
// as a data column in the base URL, which is resolved once before any virtual
// user exists.
var ErrBindingUnsupported = errors.New("source variable binding unsupported")

// Binding is how one source variable is supplied when it is not one value
// for every user.
type Binding struct {
	Mode string `json:"mode"`
	// Data: the scenario data binding alias and the pool column to read.
	Alias  string `json:"alias,omitempty"`
	Column string `json:"column,omitempty"`
	// Generated: what to generate, and the length of a digits value.
	Generator string `json:"generator,omitempty"`
	Digits    int    `json:"digits,omitempty"`
}

var dataReferenceName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Validate reports a binding that names no supported source.
func (b Binding) Validate() error {
	switch b.Mode {
	case BindingData:
		if !dataReferenceName.MatchString(b.Alias) || !dataReferenceName.MatchString(b.Column) {
			return errors.New("a data binding needs an alias and a column of letters, digits, underscores or hyphens")
		}
	case BindingGenerated:
		switch b.Generator {
		case GeneratorUUID, GeneratorTimestamp:
		case GeneratorDigits:
			if b.Digits < 1 || b.Digits > maxGeneratedDigits {
				return fmt.Errorf("generated digits need a length from 1 to %d", maxGeneratedDigits)
			}
		default:
			return errors.New("a generated value must be uuid, timestamp or digits")
		}
	default:
		return errors.New("a binding mode must be data or generated")
	}
	return nil
}

// Template is what the variable's references become: a data column read,
// or, for a generated value, the variable itself backed by a parameter.
func (b Binding) Template(name string) string {
	if b.Mode == BindingData {
		return "{{data." + b.Alias + "." + b.Column + "}}"
	}
	return "{{var." + name + "}}"
}

// generatedParameter is the parameter a generated binding defines.
func (b Binding) generatedParameter(name string) dsl.Parameter {
	switch b.Generator {
	case GeneratorUUID:
		return dsl.Parameter{Name: name, Type: "uuid", Update: "each_iteration"}
	case GeneratorTimestamp:
		return dsl.Parameter{Name: name, Type: "timestamp", Format: "iso8601", Update: "each_iteration"}
	default:
		return dsl.Parameter{Name: name, Type: "random_string", Length: b.Digits, Charset: "0123456789", Update: "each_iteration"}
	}
}

// Bind supplies a source variable candidate in one script, which must be one
// of the candidate's scripts. A data binding rewrites every {{var.NAME}}
// reference to the column and clears the gate the missing variable set; a
// generated binding defines the parameter those references read. A variable
// the binding cannot reach everywhere it is used is refused, not half bound.
func Bind(script *dsl.Script, candidate Candidate, binding Binding) ([]Diff, error) {
	if script == nil {
		return nil, nil
	}
	if candidate.Kind != KindSourceVariable {
		return nil, fmt.Errorf("%w: %s is not a variable the source left unset", ErrBindingUnsupported, candidate.Name)
	}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	variable := candidate.SourceVariable
	if variable == "" {
		variable = candidate.Name
	}
	reference := regexp.MustCompile(`\{\{\s*var\.` + regexp.QuoteMeta(variable) + `\s*\}\}`)
	if reference.MatchString(script.Config.BaseURL) {
		return nil, fmt.Errorf("%w: %s is part of the base URL, which is set once before any virtual user starts; give it one value for every user", ErrBindingUnsupported, candidate.Name)
	}
	var diffs []Diff
	if binding.Mode == BindingGenerated {
		param := binding.generatedParameter(candidate.Name)
		if index := parameterIndex(script, candidate.Name); index >= 0 {
			script.Parameters[index] = param
		} else {
			script.Parameters = append(script.Parameters, param)
		}
		diffs = append(diffs, Diff{CandidateID: candidate.ID, ScriptName: scriptName(script), Path: "parameters", After: param.Type})
	} else {
		template := binding.Template(candidate.Name)
		err := walkFields(script, func(entry field) bool {
			if next := reference.ReplaceAllLiteralString(entry.value, template); next != entry.value {
				entry.set(next)
				diffs = append(diffs, Diff{CandidateID: candidate.ID, ScriptName: scriptName(script), Path: entry.path + "/" + entry.name, After: template})
			}
			return true
		})
		if err != nil {
			return nil, err
		}
	}
	for _, missing := range requestimport.MissingVariables(script, nil) {
		if NormalizeName(missing) == candidate.Name {
			return nil, fmt.Errorf("%w: %s is also used where a data column cannot be read; give it one value for every user", ErrBindingUnsupported, candidate.Name)
		}
	}
	if _, err := requestimport.ClearResolvedMissingVariableGates(script); err != nil {
		return nil, err
	}
	return diffs, nil
}
