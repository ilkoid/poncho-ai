// Chain-cli — CLI утилита для тестирования Chain Pattern.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/chain"
)

// printHuman выводит результат в человекочитаемом формате.
func printHuman(output chain.ChainOutput, noColor bool) {
	fmt.Println("=== Chain Execution ===")
	fmt.Println()
	fmt.Printf("Query: %s\n", output.Result)
	fmt.Println()

	if output.Iterations > 0 {
		fmt.Printf("Iterations: %d\n", output.Iterations)
		fmt.Printf("Duration: %d ms\n", output.Duration.Milliseconds())
	}
}

// printJSON выводит результат в JSON формате.
func printJSON(output chain.ChainOutput, noColor bool) {
	result := struct {
		Query     string `json:"query"`
		Result    string `json:"result"`
		Iterations int    `json:"iterations"`
		DurationMs int64  `json:"duration_ms"`
		DebugLog   string `json:"debug_log,omitempty"`
		Success    bool   `json:"success"`
	}{
		Query:     "", // TODO: передать query в ChainOutput
		Result:    output.Result,
		Iterations: output.Iterations,
		DurationMs: output.Duration.Milliseconds(),
		DebugLog:   output.DebugPath,
		Success:    true,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		return
	}

	fmt.Println(string(data))
}

// printDetailed выводит детальную информацию о выполнении.
func printDetailed(output chain.ChainOutput, noColor bool) {
	fmt.Println("=== Chain Execution ===")
	fmt.Println()

	fmt.Printf("Iterations: %d\n", output.Iterations)
	fmt.Printf("Duration: %v\n", output.Duration)
	fmt.Println()

	if len(output.FinalState) > 0 {
		fmt.Println("=== Messages ===")
		for i, msg := range output.FinalState {
			role := string(msg.Role)
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			fmt.Printf("%d. [%s] %s\n", i+1, role, content)
		}
		fmt.Println()
	}

	fmt.Println("=== Result ===")
	fmt.Println(output.Result)
	fmt.Println()

	if output.DebugPath != "" {
		fmt.Printf("Debug log: %s\n", output.DebugPath)
	}
}

// formatDuration форматирует длительность в человекочитаемый формат.
func formatDuration(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	sec := ms / 1000
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	min := sec / 60
	sec = sec % 60
	return fmt.Sprintf("%dm %ds", min, sec)
}

// truncate обрезает строку до указанной длины.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// indent добавляет отступы к каждой строке.
func indent(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
