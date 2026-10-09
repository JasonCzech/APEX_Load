package pagecontext

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrNotFound means nothing on the page matched a step, as VuGen reports a
// missing link, image or form.
var ErrNotFound = errors.New("not found on the current page")

// FindLink returns the URL of the ordinal-th link (1-based; 0 means 1) whose
// visible text equals text after whitespace normalization.
func (p *Page) FindLink(text string, ordinal int, caseInsensitive bool) (string, error) {
	if p == nil {
		return "", fmt.Errorf("link %q: %w (no HTML page has been loaded)", text, ErrNotFound)
	}
	want := normalizeText(text)
	seen := 0
	for _, link := range p.Links {
		if equalText(link.Text, want, caseInsensitive) {
			seen++
			if seen == max(ordinal, 1) {
				return link.Href, nil
			}
		}
	}
	return "", fmt.Errorf("link %q (ordinal %d): %w; the page has %d link(s) with that text", text, max(ordinal, 1), ErrNotFound, seen)
}

// ImageTarget is what clicking an image does: follow Href, or submit the
// form at index Form with the image's Name coordinates.
type ImageTarget struct {
	Href string
	Form int
	Name string
}

// FindImage resolves the ordinal-th image matching alt and/or src. A linked
// image follows its anchor; an <input type=image> submits its form; an image
// map with exactly one area follows that area.
func (p *Page) FindImage(alt, src string, ordinal int, caseInsensitive bool) (ImageTarget, error) {
	if p == nil {
		return ImageTarget{}, fmt.Errorf("image: %w (no HTML page has been loaded)", ErrNotFound)
	}
	if alt == "" && src == "" {
		return ImageTarget{}, errors.New("image step needs Alt or Src")
	}
	resolvedSrc := p.resolve(src)
	seen := 0
	for _, image := range p.Images {
		if alt != "" && !equalText(normalizeText(image.Alt), normalizeText(alt), caseInsensitive) {
			continue
		}
		if src != "" && image.RawSrc != src && image.Src != resolvedSrc {
			continue
		}
		seen++
		if seen != max(ordinal, 1) {
			continue
		}
		switch {
		case image.Form >= 0:
			return ImageTarget{Form: image.Form, Name: image.Name}, nil
		case image.LinkHref != "":
			return ImageTarget{Href: image.LinkHref, Form: -1}, nil
		case image.UseMap != "":
			var areas []Area
			for _, area := range p.Areas {
				if strings.EqualFold(area.Map, image.UseMap) {
					areas = append(areas, area)
				}
			}
			if len(areas) == 1 {
				return ImageTarget{Href: areas[0].Href, Form: -1}, nil
			}
			return ImageTarget{}, fmt.Errorf("image uses map %q with %d areas; the step does not say which area was clicked", image.UseMap, len(areas))
		default:
			return ImageTarget{}, errors.New("the matching image is not a link, an image-map area or a form submit button")
		}
	}
	return ImageTarget{}, fmt.Errorf("image (alt %q, src %q, ordinal %d): %w", alt, src, max(ordinal, 1), ErrNotFound)
}

// HasImage reports whether an image matching alt and/or src is on the page.
func (p *Page) HasImage(alt, src string, caseInsensitive bool) bool {
	if p == nil {
		return false
	}
	resolvedSrc := p.resolve(src)
	for _, image := range p.Images {
		if alt != "" && !equalText(normalizeText(image.Alt), normalizeText(alt), caseInsensitive) {
			continue
		}
		if src != "" && image.RawSrc != src && image.Src != resolvedSrc {
			continue
		}
		return true
	}
	return false
}

// FindForm selects a form as VuGen does: the forms whose controls include
// every scripted field name, then those whose action matches, then the
// ordinal-th of what is left (1-based; 0 means 1).
func (p *Page) FindForm(fieldNames []string, action string, ordinal int) (int, error) {
	if p == nil || len(p.Forms) == 0 {
		return -1, fmt.Errorf("form: %w (the page has no forms)", ErrNotFound)
	}
	candidates := make([]int, 0, len(p.Forms))
	for index, form := range p.Forms {
		if form.hasAllFields(fieldNames) {
			candidates = append(candidates, index)
		}
	}
	if len(candidates) == 0 {
		return -1, fmt.Errorf("form with fields %s: %w", strings.Join(fieldNames, ", "), ErrNotFound)
	}
	if action = strings.TrimSpace(action); action != "" && len(candidates) > 1 {
		resolved := p.resolve(action)
		var byAction []int
		for _, index := range candidates {
			if form := p.Forms[index]; form.Action == resolved || strings.Contains(form.Action, action) {
				byAction = append(byAction, index)
			}
		}
		if len(byAction) > 0 {
			candidates = byAction
		}
	}
	if max(ordinal, 1) > len(candidates) {
		return -1, fmt.Errorf("form ordinal %d: %w (%d matching form(s))", ordinal, ErrNotFound, len(candidates))
	}
	return candidates[max(ordinal, 1)-1], nil
}

func (f Form) hasAllFields(names []string) bool {
	for _, name := range names {
		found := false
		for _, control := range f.Controls {
			// A recorded image-button click is scripted as name.x and name.y.
			if control.Name == name || (control.Type == "image" && (name == control.Name+".x" || name == control.Name+".y")) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Field is one name/value pair of a form submission.
type Field struct {
	Name  string
	Value string
}

// Submission builds a form's entry list with the HTML form-submission rules:
// the successful controls in document order (enabled, named; checkboxes and
// radios only when checked; selects by their selected options; no buttons),
// then the k-th scripted value of a name replaces the k-th entry of that name
// or is appended, then fallback values fill names the page does not have (recorded
// hidden values), and the submitter's fields come last.
func (f Form) Submission(scripted, fallback, submitter []Field) []Field {
	var entries []Field
	for _, control := range f.Controls {
		if control.Disabled || control.Name == "" {
			continue
		}
		switch control.Type {
		case "submit", "image", "reset", "button":
			continue
		case "checkbox", "radio":
			if control.Checked {
				value := control.Value
				if value == "" {
					value = "on"
				}
				entries = append(entries, Field{Name: control.Name, Value: value})
			}
		case "select":
			selected := 0
			for _, option := range control.Options {
				if option.Selected && !option.Disabled {
					entries = append(entries, Field{Name: control.Name, Value: option.Value})
					selected++
				}
			}
			if selected == 0 && !control.Multiple {
				for _, option := range control.Options {
					if !option.Disabled {
						entries = append(entries, Field{Name: control.Name, Value: option.Value})
						break
					}
				}
			}
		case "textarea":
			entries = append(entries, Field{Name: control.Name, Value: strings.ReplaceAll(strings.ReplaceAll(control.Value, "\r\n", "\n"), "\n", "\r\n")})
		default:
			entries = append(entries, Field{Name: control.Name, Value: control.Value})
		}
	}
	// The k-th scripted value for a name replaces the k-th entry of that
	// name, so repeated names (field=a, field=b) fill successive controls.
	used := map[string]int{}
	for _, field := range scripted {
		occurrence := used[field.Name]
		used[field.Name]++
		replaced := false
		seen := 0
		for index := range entries {
			if entries[index].Name != field.Name {
				continue
			}
			if seen == occurrence {
				entries[index].Value = field.Value
				replaced = true
				break
			}
			seen++
		}
		if !replaced {
			entries = append(entries, field)
		}
	}
	for _, field := range fallback {
		present := false
		for _, entry := range entries {
			if entry.Name == field.Name {
				present = true
				break
			}
		}
		if !present {
			entries = append(entries, field)
		}
	}
	return append(entries, submitter...)
}

func (p *Page) resolve(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || p.Base == nil {
		return raw
	}
	resolved, err := p.Base.Parse(raw)
	if err != nil {
		return raw
	}
	resolved.Fragment = ""
	return resolved.String()
}

func equalText(a, b string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// EncodeURLForm encodes fields as application/x-www-form-urlencoded.
func EncodeURLForm(fields []Field) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, url.QueryEscape(field.Name)+"="+url.QueryEscape(field.Value))
	}
	return strings.Join(parts, "&")
}
