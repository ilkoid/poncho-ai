package main

import "encoding/json"

// OneCRestItem — top-level item from the RESTs API.
// Each item has a good_guid and an array of SKU objects.
type OneCRestItem struct {
	GoodGUID string            `json:"good_guid"`
	SKU      []json.RawMessage `json:"sku"`
}

// OneCStorageRow — a single storage record inside a SKU entry.
type OneCStorageRow struct {
	StorageGUID string `json:"storage_guid"`
	StorageName string `json:"storage_name"`
	Stock       int    `json:"stock"`
	Reserv      int    `json:"reserv"`
	Free        int    `json:"free"`
	FirstStage  bool   `json:"first_stage"`
}
