// Package main provides WB Sales Downloader utility.
// This file contains shared helper functions.
package main

// repeat repeats string n times.
func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
