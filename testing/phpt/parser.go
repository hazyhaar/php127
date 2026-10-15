// Package phpt executes the supported PHPT sections without normalizing output.
package phpt

import (
	"bytes"
	"fmt"
	"strings"
)

// Test retains section contents, including their final line endings.
type Test struct {
	Name                   string
	Code, SkipIf, Expected []byte
	External               string
	ExpectKind             string
	HasSkipIf              bool
}

// Parse rejects ambiguous files and unsupported sections rather than ignoring them.
func Parse(data []byte) (Test, error) {
	var test Test
	sections := make(map[string][]byte)
	current, start := "", 0
	for pos := 0; pos < len(data); {
		end := bytes.IndexByte(data[pos:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += pos + 1
		}
		line := strings.TrimSuffix(strings.TrimSuffix(string(data[pos:end]), "\n"), "\r")
		isSection := strings.HasPrefix(line, "--") && strings.HasSuffix(line, "--") && len(line) > 4
		if isSection {
			for _, value := range line[2 : len(line)-2] {
				if value != '_' && (value < 'A' || value > 'Z') {
					isSection = false
					break
				}
			}
		}
		if isSection {
			section := line[2 : len(line)-2]
			switch section {
			case "TEST", "SKIPIF", "FILE", "FILE_EXTERNAL", "EXPECT", "EXPECTF", "EXPECTREGEX":
			default:
				return test, fmt.Errorf("unsupported PHPT section %q", section)
			}
			if current != "" {
				sections[current] = bytes.Clone(data[start:pos])
			} else if pos != 0 || section != "TEST" {
				return test, fmt.Errorf("PHPT must begin with --TEST--")
			}
			if _, exists := sections[section]; exists {
				return test, fmt.Errorf("duplicate PHPT section %s", section)
			}
			sections[section] = nil
			current, start = section, end
		} else if current == "" {
			return test, fmt.Errorf("content before --TEST--")
		}
		pos = end
	}
	if current != "" {
		sections[current] = bytes.Clone(data[start:])
	}
	if strings.TrimSpace(string(sections["TEST"])) == "" {
		return test, fmt.Errorf("missing or empty TEST section")
	}
	_, hasFile := sections["FILE"]
	_, hasExternal := sections["FILE_EXTERNAL"]
	if hasFile == hasExternal {
		return test, fmt.Errorf("exactly one FILE or FILE_EXTERNAL section is required")
	}
	for _, kind := range []string{"EXPECT", "EXPECTF", "EXPECTREGEX"} {
		if expected, ok := sections[kind]; ok {
			if test.ExpectKind != "" {
				return test, fmt.Errorf("multiple expectation sections")
			}
			test.ExpectKind, test.Expected = kind, expected
		}
	}
	if test.ExpectKind == "" {
		return test, fmt.Errorf("missing expectation section")
	}
	test.Name, test.Code = string(sections["TEST"]), sections["FILE"]
	test.SkipIf, test.HasSkipIf = sections["SKIPIF"]
	if hasExternal {
		test.External = strings.TrimSpace(string(sections["FILE_EXTERNAL"]))
		if test.External == "" || strings.ContainsAny(test.External, "\r\n\x00") {
			return test, fmt.Errorf("FILE_EXTERNAL must contain one nonempty path")
		}
	}
	return test, nil
}
