package cardupdate

import (
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestSmartMerge_NoChanges(t *testing.T) {
	current := []CardChar{
		{CharID: 1, Value: `["синий"]`},
		{CharID: 2, Value: `[42]`},
	}
	result := SmartMerge(current, nil, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 chars, got %d", len(result))
	}
	if result[0].ID != 1 || result[0].Value != "синий" {
		t.Errorf("char 1: got %v", result[0])
	}
	if result[1].ID != 2 || result[1].Value != 42 {
		t.Errorf("char 2: got %v", result[1])
	}
}

func TestSmartMerge_WithChanges(t *testing.T) {
	current := []CardChar{
		{CharID: 1, Value: `["синий"]`},
		{CharID: 2, Value: `[42]`},
	}
	changes := []CharChange{
		{CharID: 1, Value: "красный"},
	}
	result := SmartMerge(current, changes, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 chars, got %d", len(result))
	}

	// Char 1 should be changed
	if result[0].ID != 1 {
		t.Errorf("char 1 ID: got %d", result[0].ID)
	}
	got, ok := result[0].Value.([]string)
	if !ok || got[0] != "красный" {
		t.Errorf("char 1 value: got %v, want []string{\"красный\"}", result[0].Value)
	}

	// Char 2 should be preserved
	if result[1].ID != 2 || result[1].Value != 42 {
		t.Errorf("char 2: got %v, want ID=2 Value=42", result[1])
	}
}

func TestSmartMerge_ProtectedIDs(t *testing.T) {
	current := []CardChar{
		{CharID: 1, Value: `["синий"]`},
		{CharID: 2, Value: `[42]`},
	}
	changes := []CharChange{
		{CharID: 1, Value: "красный"},
		{CharID: 2, Value: "100"},
	}
	protected := map[int]bool{1: true}
	result := SmartMerge(current, changes, protected)

	// Char 1 is protected — must keep original
	if result[0].ID != 1 || result[0].Value != "синий" {
		t.Errorf("protected char 1: got %v, want Value=синий", result[0])
	}

	// Char 2 is not protected — should be changed
	if result[1].ID != 2 || result[1].Value != 100 {
		t.Errorf("char 2: got %v, want Value=100", result[1])
	}
}

func TestSmartMerge_NewFields(t *testing.T) {
	current := []CardChar{
		{CharID: 1, Value: `["синий"]`},
	}
	changes := []CharChange{
		{CharID: 99, Value: "новое поле"},
	}
	result := SmartMerge(current, changes, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 chars, got %d: %+v", len(result), result)
	}

	// Find the new char
	var found *wb.CardUpdateCharc
	for i := range result {
		if result[i].ID == 99 {
			found = &result[i]
			break
		}
	}
	if found == nil {
		t.Fatal("new char ID 99 not found in result")
	}
	got, ok := found.Value.([]string)
	if !ok || got[0] != "новое поле" {
		t.Errorf("new char value: got %v, want []string{\"новое поле\"}", found.Value)
	}
}

func TestSmartMerge_EmptyCurrent(t *testing.T) {
	changes := []CharChange{
		{CharID: 1, Value: "значение"},
		{CharID: 2, Value: "другое"},
	}
	result := SmartMerge(nil, changes, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 chars, got %d", len(result))
	}
	for _, r := range result {
		arr, ok := r.Value.([]string)
		if !ok || len(arr) == 0 {
			t.Errorf("char %d: got %v, want []string", r.ID, r.Value)
		}
	}
}
