package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type recordArgs struct {
	out string
}

func parseRecordArgs(args []string) recordArgs {
	r := recordArgs{
		out: "recording.json",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out", "-o":
			if i+1 < len(args) {
				i++
				r.out = args[i]
			}
		}
	}
	return r
}

func recordCmd() {
	if len(os.Args) < 3 || os.Args[2] != "browser" {
		fmt.Fprintln(os.Stderr, "Usage: apexctl record browser [--out recording.json]")
		os.Exit(1)
	}
	args := parseRecordArgs(os.Args[3:])

	fmt.Println("Launching Playwright inspector for browser recording...")
	fmt.Println("Perform your actions in the browser window. Close the inspector when finished.")

	tmpFile := "playwright_recording_tmp.js"
	defer os.Remove(tmpFile)

	// Run npx playwright codegen
	cmd := exec.Command("npx", "playwright", "codegen", "--target=javascript", "-o", tmpFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Recording failed or interrupted: %v\n", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read recording output: %v\n", err)
		os.Exit(1)
	}

	script := convertPlaywrightToDSL(string(content))
	
	outBytes, err := json.MarshalIndent(script, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to encode DSL: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(args.out, outBytes, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write script %s: %v\n", args.out, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully recorded APEX script to %s\n", args.out)
}

func convertPlaywrightToDSL(js string) map[string]any {
	lines := strings.Split(js, "\n")
	
	actions := []map[string]any{}
	
	gotoRegex := regexp.MustCompile(`page\.goto\(['"]([^'"]+)['"]\)`)
	clickRegex := regexp.MustCompile(`page\.locator\(['"]([^'"]+)['"]\)\.click\(\)`)
	fillRegex := regexp.MustCompile(`page\.locator\(['"]([^'"]+)['"]\)\.fill\(['"]([^'"]+)['"]\)`)
	selectRegex := regexp.MustCompile(`page\.locator\(['"]([^'"]+)['"]\)\.selectOption\(['"]([^'"]+)['"]\)`)

	for _, line := range lines {
		if match := gotoRegex.FindStringSubmatch(line); match != nil {
			actions = append(actions, map[string]any{
				"name": "Navigate to " + match[1],
				"type": "browser_navigate",
				"target": match[1],
			})
		} else if match := clickRegex.FindStringSubmatch(line); match != nil {
			actions = append(actions, map[string]any{
				"name": "Click " + match[1],
				"type": "browser_click",
				"selector": match[1],
			})
		} else if match := fillRegex.FindStringSubmatch(line); match != nil {
			actions = append(actions, map[string]any{
				"name": "Fill " + match[1],
				"type": "browser_fill",
				"selector": match[1],
				"value": match[2],
			})
		} else if match := selectRegex.FindStringSubmatch(line); match != nil {
			actions = append(actions, map[string]any{
				"name": "Select " + match[1],
				"type": "browser_select",
				"selector": match[1],
				"value": match[2],
			})
		}
	}

	return map[string]any{
		"name": "Recorded Browser Script",
		"protocol": "browser",
		"config": map[string]any{
			"browser_engine": "chromium",
			"browser_headless": false,
		},
		"actions": actions,
	}
}
