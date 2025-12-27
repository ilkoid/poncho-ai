package utils

import (
	"testing"
)

func TestCleanJsonBlock(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain JSON",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON in markdown code block",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON in lowercase markdown",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with extra text at start - cleaned only ``` at end",
			input:    "```json\n{\"key\": \"value\"}\n``` Конец",
			expected: "{\"key\": \"value\"}\n``` Конец",
		},
		{
			name:     "JSON with mixed case",
			input:    "```JSON\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with only triple backticks",
			input:    "```\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with extra whitespace",
			input:    "  ```json  \n  {\"key\": \"value\"}  \n  ```  ",
			expected: `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanJsonBlock(tt.input)
			if result != tt.expected {
				t.Errorf("CleanJsonBlock() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestCleanMarkdownCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "Just plain text",
			expected: "Just plain text",
		},
		{
			name:     "text with code block",
			input:    "Example:\n```\ncode here\n```\nDone",
			expected: "Example:\nDone",
		},
		{
			name:     "multiple code blocks",
			input:    "```\nfirst\n```\ntext\n```\nsecond\n```",
			expected: "text",
		},
		{
			name:     "json code block",
			input:    "Result:\n```json\n{\"a\": 1}\n```\nEnd",
			expected: "Result:\nEnd",
		},
		{
			name:     "inline code not removed",
			input:    "Use `var` for variables",
			expected: "Use `var` for variables",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanMarkdownCode(tt.input)
			if result != tt.expected {
				t.Errorf("CleanMarkdownCode() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSanitizeLLMOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "Simple text",
			expected: "Simple text",
		},
		{
			name:     "text with extra spaces",
			input:    "  Line 1  \n  Line 2  ",
			expected: "Line 1\nLine 2",
		},
		{
			name:     "text with code blocks",
			input:    "```\ncode\n```\ntext",
			expected: "text",
		},
		{
			name:     "multiline with empty lines",
			input:    "Line 1\n\n\nLine 2\n\nLine 3",
			expected: "Line 1\nLine 2\nLine 3",
		},
		{
			name:     "empty lines at start",
			input:    "\n\n\nStart here",
			expected: "Start here",
		},
		{
			name:     "empty lines at end",
			input:    "End here\n\n\n",
			expected: "End here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeLLMOutput(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeLLMOutput() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "pure JSON",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with text before",
			input:    "Here is the result: {\"key\": \"value\"}",
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with text after",
			input:    "{\"key\": \"value\"} That's all",
			expected: `{"key": "value"}`,
		},
		{
			name:     "nested JSON",
			input:    "Result: {\"outer\": {\"inner\": 1}} done",
			expected: `{"outer": {"inner": 1}}`,
		},
		{
			name:     "no JSON",
			input:    "Just plain text",
			expected: "",
		},
		{
			name:     "JSON array",
			input:    "[{\"a\": 1}, {\"b\": 2}]",
			expected: "",
		}, // Only supports objects starting with {
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractJSON(tt.input)
			if result != tt.expected {
				t.Errorf("ExtractJSON() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSplitChunks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		separator string
		expected  []string
	}{
		{
			name:      "double newline",
			input:     "a\n\nb\n\nc",
			separator: "\n\n",
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "dash separator",
			input:     "a---b---c",
			separator: "---",
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "empty chunks removed",
			input:     "a\n\n\n\nb",
			separator: "\n\n",
			expected:  []string{"a", "b"},
		},
		{
			name:      "single chunk",
			input:     "single",
			separator: "\n\n",
			expected:  []string{"single"},
		},
		{
			name:      "empty input",
			input:     "",
			separator: "\n\n",
			expected:  []string{},
		},
		{
			name:      "empty separator",
			input:     "a\nb",
			separator: "",
			expected:  []string{"a\nb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitChunks(tt.input, tt.separator)
			if len(result) != len(tt.expected) {
				t.Errorf("SplitChunks() length = %d, want %d", len(result), len(tt.expected))
				return
			}
			for i, chunk := range result {
				if chunk != tt.expected[i] {
					t.Errorf("SplitChunks()[%d] = %q, want %q", i, chunk, tt.expected[i])
				}
			}
		})
	}
}

func TestTrimCommonPrefixes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bullet list",
			input:    "- item 1\n- item 2\n- item 3",
			expected: "item 1\nitem 2\nitem 3",
		},
		{
			name:     "numbered list - no common prefix",
			input:    "1. first\n2. second\n3. third",
			expected: "1. first\n2. second\n3. third", // Нет общего префикса, поэтому не удаляется
		},
		{
			name:     "no common prefix",
			input:    "first\nsecond\nthird",
			expected: "first\nsecond\nthird",
		},
		{
			name:     "mixed spaces and dashes",
			input:    "  - item1\n  - item2",
			expected: "item1\nitem2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TrimCommonPrefixes(tt.input)
			if result != tt.expected {
				t.Errorf("TrimCommonPrefixes() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		width    int
		expected string
	}{
		{
			name:     "simple wrap",
			input:    "hello world",
			width:    5,
			expected: "hello\nworld",
		},
		{
			name:     "preserves existing newlines",
			input:    "a\nb\nc",
			width:    10,
			expected: "a\nb\nc",
		},
		{
			name:     "no wrap needed",
			input:    "short text",
			width:    20,
			expected: "short text",
		},
		{
			name:     "empty string",
			input:    "",
			width:    10,
			expected: "",
		},
		{
			name:     "width less than 1 returns original",
			input:    "hello world",
			width:    0,
			expected: "hello world",
		},
		{
			name:     "preserves empty lines",
			input:    "line1\n\nline2",
			width:    10,
			expected: "line1\n\nline2",
		},
		{
			name:     "long word stays intact",
			input:    "supercalifragilisticexpialidocious",
			width:    10,
			expected: "supercalifragilisticexpialidocious",
		},
		{
			name:     "multiple words wrap",
			input:    "one two three four five",
			width:    10,
			expected: "one two\nthree four\nfive",
		},
		{
			name:     "multiline with long lines",
			input:    "first line is very long and should wrap\nsecond line also very long",
			width:    15,
			expected: "first line is\nvery long and\nshould wrap\nsecond line\nalso very long",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapText(tt.input, tt.width)
			if result != tt.expected {
				t.Errorf("WrapText() = %q, want %q", result, tt.expected)
			}
		})
	}
}
