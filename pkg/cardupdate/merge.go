// Smart merge logic for WB card characteristics.
//
// When updating card characteristics via WB API, the entire card is replaced.
// SmartMerge preserves existing values for unchanged and protected characteristics
// while applying targeted changes. This prevents accidental data loss.
//
// Algorithm (battle-tested in fix-card-fields and check-card-consistency):
//  1. Iterate current characteristics — keep protected, apply matching changes, preserve rest
//  2. Add new characteristics absent from the card
package cardupdate

import (
	"encoding/json"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CardChar is one characteristic row from the card_characteristics table.
// Used as the intermediate representation between raw DB rows and wb.CardUpdateCharc.
// Value is stored as raw JSON (e.g., ["text"] or [42]).
type CardChar struct {
	CharID int
	Name   string
	Value  string
}

// CharChange represents a single characteristic replacement.
// Value is the new value as a plain string — type conversion is handled by SmartMerge.
type CharChange struct {
	CharID int
	Value  string
}

// SmartMerge merges current card characteristics with requested changes,
// protecting specified characteristic IDs from modification.
//
// The merge preserves existing values by unwrapping them from DB format,
// applies type-aware conversion for changed values, and adds new fields
// that were previously absent from the card.
func SmartMerge(
	currentChars []CardChar,
	changes []CharChange,
	protectedIDs map[int]bool,
) []wb.CardUpdateCharc {
	changesMap := make(map[int]string, len(changes))
	for _, ch := range changes {
		changesMap[ch.CharID] = ch.Value
	}

	var finalChars []wb.CardUpdateCharc
	seenIDs := make(map[int]bool, len(currentChars))

	// Pass 1: iterate all current characteristics.
	for _, curr := range currentChars {
		seenIDs[curr.CharID] = true

		var val any
		if err := json.Unmarshal([]byte(curr.Value), &val); err != nil {
			val = curr.Value
		}
		val = UnwrapValue(val)

		if protectedIDs[curr.CharID] {
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
		} else if newVal, exists := changesMap[curr.CharID]; exists {
			convertedValue := ConvertCharValue(newVal, curr.Value)
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: convertedValue})
		} else {
			finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
		}
	}

	// Pass 2: add new fields absent from current characteristics.
	for charID, newVal := range changesMap {
		if !seenIDs[charID] {
			finalChars = append(finalChars, wb.CardUpdateCharc{
				ID:    charID,
				Value: StringToCharArray(newVal),
			})
		}
	}

	return finalChars
}
