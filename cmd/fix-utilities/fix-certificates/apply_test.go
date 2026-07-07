package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestKizMarkedPayloadSerialization guards the 3-value logic of wb.CardUpdateItem.KizMarked:
// buildSmartMergePayload must carry an explicit *bool (sourced from cards.kiz_marked) so an
// existing "Честный ЗНАК" marking state survives the full-card rewrite, while NULL must stay
// omitted (WB then applies default false).
func TestKizMarkedPayloadSerialization(t *testing.T) {
	base := wb.CardUpdateItem{NmID: 1, VendorCode: "VC", Brand: "B", Title: "T"}

	t.Run("explicit_true_emitted", func(t *testing.T) {
		v := true
		item := base
		item.KizMarked = &v
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte(`"kizMarked":true`)) {
			t.Errorf("expected kizMarked:true in payload, got: %s", b)
		}
	})

	t.Run("explicit_false_emitted", func(t *testing.T) {
		v := false
		item := base
		item.KizMarked = &v
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte(`"kizMarked":false`)) {
			t.Errorf("expected kizMarked:false in payload, got: %s", b)
		}
	})

	t.Run("nil_omitted", func(t *testing.T) {
		item := base // KizMarked == nil
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(`kizMarked`)) {
			t.Errorf("expected kizMarked omitted when nil (cards.kiz_marked IS NULL), got: %s", b)
		}
	})
}
