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
// virtual user, a value generated each iteration, or a project secret (the
// way a credential is supplied).
const (
	BindingData      = "data"
	BindingGenerated = "generated"
	BindingSecret    = "secret"

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
	// Secret: the project secret the variable reads.
	Secret string `json:"secret,omitempty"`
}

var dataReferenceName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// projectSecretName is a logical project secret name, as the secret store
// and {{secret.NAME}} references spell it.
var projectSecretName = regexp.MustCompile(`^[A-Z0-9_]{1,249}$`)

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
	case BindingSecret:
		if !projectSecretName.MatchString(b.Secret) {
			return errors.New("a project secret name uses capital letters, digits and underscores")
		}
	default:
		return errors.New("a binding mode must be data, generated or secret")
	}
	return nil
}

// ValidateFor reports a binding that names no supported source, or one a
// credential-shaped input cannot take: a credential reads a project secret or
// a data pool column, never a generated value or one kept in the script.
// Bind applies it, so every importer refuses the same bindings.
func (b Binding) ValidateFor(candidate Candidate) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if candidate.CredentialShaped && b.Mode != BindingData && b.Mode != BindingSecret {
		return fmt.Errorf("%s looks like a credential: supply it from a project secret or a data pool column", candidate.Name)
	}
	return nil
}

// Input is how a script records that this binding supplies the input name
// (script.metadata.import.inputs), so a later import can offer it again.
func (b Binding) Input(name string) dsl.ImportInput {
	input := dsl.ImportInput{Mode: b.Mode}
	switch b.Mode {
	case BindingData:
		input.Alias, input.Column = b.Alias, b.Column
	case BindingGenerated:
		input.Generator, input.Digits = b.Generator, b.Digits
	case BindingSecret:
		input.Reference = b.Template(name)
	}
	return input
}

// Template is what the variable's references become: a data column read,
// or, for a generated value, the variable itself backed by a parameter.
func (b Binding) Template(name string) string {
	switch b.Mode {
	case BindingData:
		return "{{data." + b.Alias + "." + b.Column + "}}"
	case BindingSecret:
		return "{{secret." + b.Secret + "}}"
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
// of the candidate's scripts. A data or secret binding rewrites every
// {{var.NAME}} reference to the column or project secret and clears the gate
// the missing variable set; a generated binding defines the parameter those
// references read. A variable
// the binding cannot reach everywhere it is used is refused, not half bound.
func Bind(script *dsl.Script, candidate Candidate, binding Binding) ([]Diff, error) {
	if script == nil {
		return nil, nil
	}
	if candidate.Kind != KindSourceVariable {
		return nil, fmt.Errorf("%w: %s is not a variable the source left unset", ErrBindingUnsupported, candidate.Name)
	}
	if err := binding.ValidateFor(candidate); err != nil {
		return nil, err
	}
	variable := candidate.SourceVariable
	if variable == "" {
		variable = candidate.Name
	}
	reference := regexp.MustCompile(`\{\{\s*var\.` + regexp.QuoteMeta(variable) + `\s*\}\}`)
	if reference.MatchString(script.Config.BaseURL) {
		return nil, fmt.Errorf("%w: %s is part of the base URL, which is set once before any virtual user starts and cannot read a %s; %s",
			ErrBindingUnsupported, candidate.Name, binding.sourceLabel(), unboundAdvice(candidate))
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
			return nil, fmt.Errorf("%w: %s is also used where a %s cannot be read; %s",
				ErrBindingUnsupported, candidate.Name, binding.sourceLabel(), unboundAdvice(candidate))
		}
	}
	if _, err := requestimport.ClearResolvedMissingVariableGates(script); err != nil {
		return nil, err
	}
	return diffs, nil
}

// sourceLabel names what a binding reads, for a refusal.
func (b Binding) sourceLabel() string {
	switch b.Mode {
	case BindingSecret:
		return "project secret"
	case BindingGenerated:
		return "generated value"
	}
	return "data column"
}

// unboundAdvice is what an operator can do with an input no binding reaches:
// one value for every user, which a credential cannot take, so a credential
// used where nothing can read it is corrected in the source.
func unboundAdvice(candidate Candidate) string {
	if candidate.CredentialShaped {
		return "correct where the source uses it and import again"
	}
	return "give it one value for every user"
}
