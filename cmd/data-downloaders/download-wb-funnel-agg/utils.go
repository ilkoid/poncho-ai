// Package main provides WB Aggregated Funnel Downloader utility.
// This file contains utility functions.
package main

// repeat creates a string by repeating a character n times.
func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
