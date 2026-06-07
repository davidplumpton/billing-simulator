package scenario

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type scenarioYAMLLine struct {
	Number int
	Indent int
	Text   string
}

type scenarioYAMLParser struct {
	lines []scenarioYAMLLine
	pos   int
}

// parseDefinitionYAMLBytes parses the small block-style YAML subset used by local scenario drafts.
func parseDefinitionYAMLBytes(data []byte) ([]byte, error) {
	lines, err := scenarioYAMLLines(data)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("scenario definition is required")
	}
	if lines[0].Indent != 0 {
		return nil, fmt.Errorf("line %d: scenario definition must start at indentation 0", lines[0].Number)
	}

	parser := scenarioYAMLParser{lines: lines}
	document, err := parser.parseBlock(0)
	if err != nil {
		return nil, err
	}
	if parser.pos != len(lines) {
		line := lines[parser.pos]
		return nil, fmt.Errorf("line %d: unexpected content %q", line.Number, line.Text)
	}
	if _, ok := document.(map[string]any); !ok {
		return nil, fmt.Errorf("scenario definition must be a JSON object or YAML mapping")
	}
	jsonData, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode scenario YAML as JSON: %w", err)
	}
	return jsonData, nil
}

// scenarioYAMLLines strips comments and blank lines while preserving source line numbers.
func scenarioYAMLLines(data []byte) ([]scenarioYAMLLine, error) {
	rawLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lines := make([]scenarioYAMLLine, 0, len(rawLines))
	for i, raw := range rawLines {
		raw = strings.TrimRight(raw, " \r\t")
		if strings.Contains(raw, "\t") {
			return nil, fmt.Errorf("line %d: tabs are not supported for scenario YAML indentation", i+1)
		}
		raw = stripScenarioYAMLComment(raw)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		text := strings.TrimSpace(raw)
		if text == "---" || text == "..." {
			return nil, fmt.Errorf("line %d: multiple YAML documents are not supported", i+1)
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		lines = append(lines, scenarioYAMLLine{
			Number: i + 1,
			Indent: indent,
			Text:   text,
		})
	}
	return lines, nil
}

func stripScenarioYAMLComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case inDouble && r == '\\':
			escaped = true
		case !inDouble && r == '\'':
			inSingle = !inSingle
		case !inSingle && r == '"':
			inDouble = !inDouble
		case !inSingle && !inDouble && r == '#' && (i == 0 || unicode.IsSpace(rune(line[i-1]))):
			return strings.TrimRight(line[:i], " ")
		}
	}
	return line
}

func (p *scenarioYAMLParser) parseBlock(indent int) (any, error) {
	if p.pos >= len(p.lines) {
		return map[string]any{}, nil
	}
	line := p.lines[p.pos]
	if line.Indent < indent {
		return map[string]any{}, nil
	}
	if line.Indent > indent {
		return nil, fmt.Errorf("line %d: unexpected indentation %d, want %d", line.Number, line.Indent, indent)
	}
	if strings.HasPrefix(line.Text, "- ") || line.Text == "-" {
		return p.parseSequence(indent)
	}
	return p.parseMapping(indent)
}

func (p *scenarioYAMLParser) parseMapping(indent int) (map[string]any, error) {
	result := map[string]any{}
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if line.Indent < indent {
			break
		}
		if line.Indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation %d, want %d", line.Number, line.Indent, indent)
		}
		if strings.HasPrefix(line.Text, "- ") || line.Text == "-" {
			break
		}

		key, valueText, ok := splitScenarioYAMLKeyValue(line.Text)
		if !ok {
			return nil, fmt.Errorf("line %d: expected key: value mapping", line.Number)
		}
		key, err := parseScenarioYAMLKey(key)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line.Number, err)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("line %d: duplicate key %q", line.Number, key)
		}

		p.pos++
		if strings.TrimSpace(valueText) == "" {
			if p.pos < len(p.lines) && p.lines[p.pos].Indent > indent {
				value, err := p.parseBlock(p.lines[p.pos].Indent)
				if err != nil {
					return nil, err
				}
				result[key] = value
			} else {
				result[key] = map[string]any{}
			}
			continue
		}
		value, err := parseScenarioYAMLScalar(valueText)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line.Number, err)
		}
		result[key] = value
	}
	return result, nil
}

func (p *scenarioYAMLParser) parseSequence(indent int) ([]any, error) {
	var result []any
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if line.Indent < indent {
			break
		}
		if line.Indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation %d, want %d", line.Number, line.Indent, indent)
		}
		if line.Text != "-" && !strings.HasPrefix(line.Text, "- ") {
			break
		}

		body := strings.TrimSpace(strings.TrimPrefix(line.Text, "-"))
		if body == "" {
			p.pos++
			if p.pos >= len(p.lines) || p.lines[p.pos].Indent <= indent {
				result = append(result, nil)
				continue
			}
			value, err := p.parseBlock(p.lines[p.pos].Indent)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
			continue
		}

		if key, valueText, ok := splitScenarioYAMLKeyValue(body); ok {
			item := map[string]any{}
			key, err := parseScenarioYAMLKey(key)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line.Number, err)
			}
			p.pos++
			if strings.TrimSpace(valueText) == "" {
				if p.pos < len(p.lines) && p.lines[p.pos].Indent > indent {
					value, err := p.parseBlock(p.lines[p.pos].Indent)
					if err != nil {
						return nil, err
					}
					item[key] = value
				} else {
					item[key] = map[string]any{}
				}
			} else {
				value, err := parseScenarioYAMLScalar(valueText)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", line.Number, err)
				}
				item[key] = value
			}
			if p.pos < len(p.lines) && p.lines[p.pos].Indent > indent {
				continuation, err := p.parseMapping(p.lines[p.pos].Indent)
				if err != nil {
					return nil, err
				}
				for continuationKey, continuationValue := range continuation {
					if _, exists := item[continuationKey]; exists {
						return nil, fmt.Errorf("line %d: duplicate key %q", line.Number, continuationKey)
					}
					item[continuationKey] = continuationValue
				}
			}
			result = append(result, item)
			continue
		}

		value, err := parseScenarioYAMLScalar(body)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line.Number, err)
		}
		p.pos++
		result = append(result, value)
	}
	return result, nil
}

func splitScenarioYAMLKeyValue(text string) (string, string, bool) {
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	for i, r := range text {
		switch {
		case escaped:
			escaped = false
		case inDouble && r == '\\':
			escaped = true
		case !inDouble && r == '\'':
			inSingle = !inSingle
		case !inSingle && r == '"':
			inDouble = !inDouble
		case !inSingle && !inDouble && (r == '{' || r == '['):
			depth++
		case !inSingle && !inDouble && (r == '}' || r == ']'):
			if depth > 0 {
				depth--
			}
		case !inSingle && !inDouble && depth == 0 && r == ':':
			if i+1 < len(text) && !unicode.IsSpace(rune(text[i+1])) {
				continue
			}
			key := strings.TrimSpace(text[:i])
			if key == "" {
				return "", "", false
			}
			return key, strings.TrimSpace(text[i+1:]), true
		}
	}
	return "", "", false
}

func parseScenarioYAMLKey(text string) (string, error) {
	value, err := parseScenarioYAMLScalar(text)
	if err != nil {
		return "", err
	}
	key, ok := value.(string)
	if !ok || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("mapping key must be a non-empty string")
	}
	return strings.TrimSpace(key), nil
}

func parseScenarioYAMLScalar(text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return parseScenarioYAMLFlowValue(text)
	}
	if strings.HasPrefix(text, `"`) || strings.HasPrefix(text, `'`) {
		return parseScenarioYAMLQuoted(text)
	}

	switch strings.ToLower(text) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null", "~":
		return nil, nil
	}
	if value, ok := parseScenarioYAMLInteger(text); ok {
		return value, nil
	}
	if value, ok := parseScenarioYAMLFloat(text); ok {
		return value, nil
	}
	return text, nil
}

func parseScenarioYAMLQuoted(text string) (string, error) {
	if strings.HasPrefix(text, `"`) {
		value, err := strconv.Unquote(text)
		if err != nil {
			return "", fmt.Errorf("invalid quoted string %q", text)
		}
		return value, nil
	}
	if !strings.HasSuffix(text, `'`) || len(text) < 2 {
		return "", fmt.Errorf("invalid quoted string %q", text)
	}
	return strings.ReplaceAll(text[1:len(text)-1], "''", "'"), nil
}

func parseScenarioYAMLInteger(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	start := 0
	if text[0] == '+' || text[0] == '-' {
		start = 1
	}
	if start == len(text) {
		return 0, false
	}
	for _, r := range text[start:] {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(text, 10, 64)
	return value, err == nil
}

func parseScenarioYAMLFloat(text string) (float64, bool) {
	if !strings.ContainsAny(text, ".eE") {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func parseScenarioYAMLFlowValue(text string) (any, error) {
	switch {
	case strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}"):
		body := strings.TrimSpace(text[1 : len(text)-1])
		result := map[string]any{}
		if body == "" {
			return result, nil
		}
		items, err := splitScenarioYAMLFlowItems(body)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key, valueText, ok := splitScenarioYAMLKeyValue(item)
			if !ok {
				return nil, fmt.Errorf("expected key: value in flow mapping %q", item)
			}
			key, err := parseScenarioYAMLKey(key)
			if err != nil {
				return nil, err
			}
			if _, exists := result[key]; exists {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			value, err := parseScenarioYAMLScalar(valueText)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil
	case strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]"):
		body := strings.TrimSpace(text[1 : len(text)-1])
		if body == "" {
			return []any{}, nil
		}
		items, err := splitScenarioYAMLFlowItems(body)
		if err != nil {
			return nil, err
		}
		result := make([]any, 0, len(items))
		for _, item := range items {
			value, err := parseScenarioYAMLScalar(item)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported flow value %q", text)
	}
}

func splitScenarioYAMLFlowItems(text string) ([]string, error) {
	var items []string
	start := 0
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	for i, r := range text {
		switch {
		case escaped:
			escaped = false
		case inDouble && r == '\\':
			escaped = true
		case !inDouble && r == '\'':
			inSingle = !inSingle
		case !inSingle && r == '"':
			inDouble = !inDouble
		case !inSingle && !inDouble && (r == '{' || r == '['):
			depth++
		case !inSingle && !inDouble && (r == '}' || r == ']'):
			if depth == 0 {
				return nil, fmt.Errorf("unbalanced flow value %q", text)
			}
			depth--
		case !inSingle && !inDouble && depth == 0 && r == ',':
			item := strings.TrimSpace(text[start:i])
			if item == "" {
				return nil, fmt.Errorf("empty flow item in %q", text)
			}
			items = append(items, item)
			start = i + 1
		}
	}
	if inSingle || inDouble || depth != 0 {
		return nil, fmt.Errorf("unbalanced flow value %q", text)
	}
	item := strings.TrimSpace(text[start:])
	if item == "" {
		return nil, fmt.Errorf("empty flow item in %q", text)
	}
	items = append(items, item)
	return items, nil
}
