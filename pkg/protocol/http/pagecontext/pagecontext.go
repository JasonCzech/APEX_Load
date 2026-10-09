// Package pagecontext indexes the HTML page a virtual user last loaded, so
// LoadRunner HTML-mode steps (web_link, web_image, web_submit_form) can be
// resolved against the live page at replay instead of a recorded URL.
//
// Only the index is kept, never the DOM: links, images and image-map areas,
// forms with every control, and frames, each in document order. The index is
// bounded so a large page cannot grow a virtual user's memory without limit.
package pagecontext

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Limits bound one page's index.
const (
	MaxLinks    = 4096
	MaxImages   = 4096
	MaxAreas    = 1024
	MaxForms    = 256
	MaxControls = 4096
	MaxFrames   = 64
	maxTextLen  = 1024
)

// Page is the index of one HTML document.
type Page struct {
	// URL is the document's final URL after redirects.
	URL string
	// Charset is the document's character encoding (Decode), in which its
	// forms are submitted.
	Charset string
	// Base resolves the page's relative URLs (<base href> when present).
	Base   *url.URL
	Links  []Link
	Images []Image
	Areas  []Area
	Forms  []Form
	Frames []Frame
	// Truncated reports that a limit cut the index short.
	Truncated bool
}

// Link is an <a href> anchor.
type Link struct {
	Text string // normalized visible text
	Href string // resolved URL
}

// Image is an <img> or an <input type=image>.
type Image struct {
	Alt string
	Src string // resolved URL
	// RawSrc is the src attribute as written.
	RawSrc string
	// LinkHref is the resolved href of the enclosing anchor, if any.
	LinkHref string
	// UseMap names the image map (without "#") the image uses.
	UseMap string
	// Form is the index of the form an <input type=image> submits, or -1.
	Form int
	// Name is an input image's name (its click submits name.x and name.y).
	Name string
}

// Area is an image-map <area href>.
type Area struct {
	Map  string
	Alt  string
	Href string // resolved URL
}

// Form is a <form> with its controls in document order.
type Form struct {
	Name     string
	ID       string
	Action   string // resolved URL; the page URL when absent
	Method   string // GET or POST
	Enctype  string
	Controls []Control
}

// Control is one form control.
type Control struct {
	Tag      string // input, select, textarea, button
	Type     string // lower-cased input type ("text" when absent)
	Name     string
	Value    string
	Checked  bool
	Disabled bool
	Options  []Option // select only
	Multiple bool     // select only
}

// Option is one <option> of a select.
type Option struct {
	Value    string
	Selected bool
	Disabled bool
}

// Frame is a <frame> or <iframe>.
type Frame struct {
	Name string
	Src  string // resolved URL
}

// Parse indexes a UTF-8 HTML document fetched from pageURL (see Decode).
func Parse(body []byte, pageURL *url.URL) *Page {
	page := &Page{Base: pageURL, Charset: "utf-8"}
	if pageURL != nil {
		page.URL = pageURL.String()
	}
	resolve := func(raw string) string {
		raw = strings.TrimSpace(raw)
		if raw == "" || page.Base == nil {
			return raw
		}
		resolved, err := page.Base.Parse(raw)
		if err != nil {
			return raw
		}
		resolved.Fragment = ""
		return resolved.String()
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	var (
		openLink     = -1 // index in page.Links of the open <a>
		openLinkHref string
		linkText     strings.Builder
		openForm     = -1
		openSelect   = -1 // index of the open select in the open form's controls
		openTextarea = -1
		textarea     strings.Builder
		openOption   = -1
		optionText   strings.Builder
		controls     = 0
		currentMap   string
	)
	closeOption := func() {
		if openOption < 0 || openForm < 0 || openSelect < 0 {
			openOption = -1
			return
		}
		option := &page.Forms[openForm].Controls[openSelect].Options[openOption]
		if option.Value == "\x00" {
			option.Value = normalizeText(optionText.String())
		}
		openOption = -1
	}
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case html.ErrorToken:
			closeOption()
			return page
		case html.TextToken:
			text := string(tokenizer.Text())
			if openLink >= 0 && linkText.Len() < maxTextLen {
				linkText.WriteString(text)
			}
			if openTextarea >= 0 && textarea.Len() < 1<<20 {
				textarea.WriteString(text)
			}
			if openOption >= 0 && optionText.Len() < maxTextLen {
				optionText.WriteString(text)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokenizer.TagName()
			attrs := map[string]string{}
			present := map[string]bool{}
			for hasAttr {
				var key, value []byte
				key, value, hasAttr = tokenizer.TagAttr()
				k := strings.ToLower(string(key))
				if _, seen := attrs[k]; !seen {
					attrs[k] = string(value)
					present[k] = true
				}
			}
			switch string(name) {
			case "base":
				if href := strings.TrimSpace(attrs["href"]); href != "" && page.Base != nil {
					if base, err := page.Base.Parse(href); err == nil {
						page.Base = base
					}
				}
			case "a":
				// An anchor cannot nest: a new one ends the open one.
				if openLink >= 0 {
					page.Links[openLink].Text = normalizeText(linkText.String())
				}
				openLink, openLinkHref = -1, ""
				if present["href"] {
					if len(page.Links) >= MaxLinks {
						page.Truncated = true
						break
					}
					openLinkHref = resolve(attrs["href"])
					page.Links = append(page.Links, Link{Href: openLinkHref})
					openLink = len(page.Links) - 1
					linkText.Reset()
				}
			case "img":
				if len(page.Images) >= MaxImages {
					page.Truncated = true
					break
				}
				image := Image{Alt: attrs["alt"], Src: resolve(attrs["src"]), RawSrc: attrs["src"], UseMap: strings.TrimPrefix(strings.TrimSpace(attrs["usemap"]), "#"), Form: -1}
				if openLink >= 0 {
					image.LinkHref = openLinkHref
				}
				page.Images = append(page.Images, image)
			case "area":
				if present["href"] {
					if len(page.Areas) >= MaxAreas {
						page.Truncated = true
						break
					}
					page.Areas = append(page.Areas, Area{Map: currentMap, Alt: attrs["alt"], Href: resolve(attrs["href"])})
				}
			case "map":
				// Areas belong to the map that encloses them.
				currentMap = attrs["name"]
				if currentMap == "" {
					currentMap = attrs["id"]
				}
			case "frame", "iframe":
				if len(page.Frames) >= MaxFrames {
					page.Truncated = true
					break
				}
				page.Frames = append(page.Frames, Frame{Name: attrs["name"], Src: resolve(attrs["src"])})
			case "form":
				if len(page.Forms) >= MaxForms {
					page.Truncated = true
					openForm = -1
					break
				}
				method := strings.ToUpper(strings.TrimSpace(attrs["method"]))
				if method != "POST" {
					method = "GET"
				}
				action := page.URL
				if present["action"] && strings.TrimSpace(attrs["action"]) != "" {
					action = resolve(attrs["action"])
				}
				enctype := strings.ToLower(strings.TrimSpace(attrs["enctype"]))
				if enctype != "multipart/form-data" && enctype != "text/plain" {
					enctype = "application/x-www-form-urlencoded"
				}
				page.Forms = append(page.Forms, Form{Name: attrs["name"], ID: attrs["id"], Action: action, Method: method, Enctype: enctype})
				openForm = len(page.Forms) - 1
			case "input", "button", "select", "textarea":
				if openForm < 0 {
					break
				}
				if controls >= MaxControls {
					page.Truncated = true
					break
				}
				controls++
				tag := string(name)
				control := Control{Tag: tag, Name: attrs["name"], Value: attrs["value"], Disabled: present["disabled"], Checked: present["checked"], Multiple: present["multiple"]}
				switch tag {
				case "input":
					control.Type = strings.ToLower(strings.TrimSpace(attrs["type"]))
					if control.Type == "" {
						control.Type = "text"
					}
					if control.Type == "image" {
						if len(page.Images) < MaxImages {
							page.Images = append(page.Images, Image{Alt: attrs["alt"], Src: resolve(attrs["src"]), RawSrc: attrs["src"], Form: openForm, Name: attrs["name"]})
						}
					}
				case "button":
					control.Type = strings.ToLower(strings.TrimSpace(attrs["type"]))
					if control.Type == "" {
						control.Type = "submit"
					}
				case "select":
					control.Type = "select"
				case "textarea":
					control.Type = "textarea"
				}
				page.Forms[openForm].Controls = append(page.Forms[openForm].Controls, control)
				index := len(page.Forms[openForm].Controls) - 1
				switch tag {
				case "select":
					openSelect = index
				case "textarea":
					openTextarea = index
					textarea.Reset()
				}
			case "option":
				closeOption()
				if openForm >= 0 && openSelect >= 0 {
					value, hasValue := attrs["value"], present["value"]
					if !hasValue {
						value = "\x00" // filled from the option's text when it closes
					}
					selectControl := &page.Forms[openForm].Controls[openSelect]
					selectControl.Options = append(selectControl.Options, Option{Value: value, Selected: present["selected"], Disabled: present["disabled"]})
					openOption = len(selectControl.Options) - 1
					optionText.Reset()
				}
			}
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			switch string(name) {
			case "a":
				if openLink >= 0 {
					page.Links[openLink].Text = normalizeText(linkText.String())
				}
				openLink, openLinkHref = -1, ""
			case "form":
				closeOption()
				openForm, openSelect, openTextarea = -1, -1, -1
			case "select":
				closeOption()
				openSelect = -1
			case "option":
				closeOption()
			case "textarea":
				if openForm >= 0 && openTextarea >= 0 {
					page.Forms[openForm].Controls[openTextarea].Value = strings.TrimPrefix(textarea.String(), "\n")
				}
				openTextarea = -1
			case "map":
				currentMap = ""
			}
		}
	}
}

// normalizeText collapses whitespace runs to one space and trims, as a
// browser renders link text. The text is already decoded (the tokenizer and
// the DOM unescape entities), so it is not unescaped again.
func normalizeText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// NormalizeText exposes the link-text normalization for matching.
func NormalizeText(text string) string { return normalizeText(text) }
