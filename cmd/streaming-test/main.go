// Streaming Test Utility
//
// CLI utility for testing streaming functionality.
// Subscribes to events and displays streaming chunks in real-time.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
)

func main() {
	// Check if query is provided
	if len(os.Args) < 2 {
		fmt.Println("Usage: streaming-test <query>")
		fmt.Println("Example: streaming-test 'Explain quantum computing'")
		os.Exit(1)
	}

	query := os.Args[1]

	// Create agent
	client, err := agent.New(agent.Config{ConfigPath: "config.yaml"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}

	// Create emitter for UI integration (ПЕРЕД Subscribe!)
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)

	// Subscribe to events (ПОСЛЕ SetEmitter!)
	sub := client.Subscribe()

	// Start goroutine to print events
	eventChan := make(chan events.Event, 100)
	go func() {
		for event := range sub.Events() {
			eventChan <- event
		}
		close(eventChan)
	}()

	fmt.Println("=== Streaming Test ===")
	fmt.Printf("Query: %s\n\n", query)
	fmt.Println("--- Events ---")

	// Start agent in background
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		result, err := client.Run(ctx, query)
		if err != nil {
			errChan <- err
		} else {
			resultChan <- result
		}
	}()

	// Process events
	lastChunk := ""
	for {
		select {
		case event := <-eventChan:
			printEvent(event, &lastChunk)

		case result := <-resultChan:
			fmt.Printf("\n--- Done ---\n")
			fmt.Printf("Final result:\n%s\n", result)
			fmt.Printf("Length: %d chars\n", len(result))
			return

		case err := <-errChan:
			fmt.Printf("\n--- Error ---\n")
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)

		case <-ctx.Done():
			fmt.Printf("\n--- Timeout ---\n")
			os.Exit(1)
		}
	}
}

func printEvent(event events.Event, lastChunk *string) {
	switch event.Type {
	case events.EventThinking:
		fmt.Printf("\n[THINKING] Started\n")

	case events.EventThinkingChunk:
		if chunkData, ok := event.Data.(events.ThinkingChunkData); ok {
			// Print delta (new chunk only)
			fmt.Print(chunkData.Chunk)
			*lastChunk = chunkData.Accumulated
		}

	case events.EventMessage:
		if content, ok := event.Data.(string); ok {
			fmt.Printf("\n[MESSAGE] %s\n", truncate(content, 100))
		}

	case events.EventToolCall:
		if toolData, ok := event.Data.(events.ToolCallData); ok {
			fmt.Printf("\n[TOOL CALL] %s(%s)\n", toolData.ToolName, truncate(toolData.Args, 50))
		}

	case events.EventToolResult:
		if resultData, ok := event.Data.(events.ToolResultData); ok {
			fmt.Printf("\n[TOOL RESULT] %s (%.2fs)\n", resultData.ToolName, float64(resultData.Duration)/float64(time.Second))
		}

	case events.EventError:
		if err, ok := event.Data.(error); ok {
			fmt.Printf("\n[ERROR] %v\n", err)
		}

	case events.EventDone:
		if result, ok := event.Data.(string); ok {
			fmt.Printf("\n[DONE] Result: %s\n", truncate(result, 100))
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
