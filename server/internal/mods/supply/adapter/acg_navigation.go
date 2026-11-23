package adapter

import (
	"encoding/json"
	"strconv"
)

// ACG API code is a docking identifier; only the separate id names /item/{id}.
func acgPageID(raw json.RawMessage) string {
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return ""
	}
	id, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil || id == 0 {
		return ""
	}
	return strconv.FormatUint(id, 10)
}
