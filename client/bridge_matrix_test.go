package client

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedBridgeContractV1Matrix(t *testing.T) {
	raw, err := os.ReadFile("testdata/bridge_contract_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Version     int `json:"version"`
		RequestKeys []struct {
			Value string `json:"value"`
			Valid bool   `json:"valid"`
		} `json:"request_keys"`
		SelectionBase map[string]json.RawMessage `json:"selection_base"`
		Selections    []struct {
			Name  string                     `json:"name"`
			Patch map[string]json.RawMessage `json:"patch"`
			Valid bool                       `json:"valid"`
		} `json:"selections"`
	}
	if err = json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	if matrix.Version != 1 {
		t.Fatal("unexpected matrix version")
	}
	for _, tc := range matrix.RequestKeys {
		if got := requestKeyPattern.MatchString(tc.Value); got != tc.Valid {
			t.Errorf("request key %q valid=%v want=%v", tc.Value, got, tc.Valid)
		}
	}
	for _, tc := range matrix.Selections {
		t.Run(tc.Name, func(t *testing.T) {
			body := map[string]json.RawMessage{}
			for k, v := range matrix.SelectionBase {
				body[k] = v
			}
			for k, v := range tc.Patch {
				body[k] = v
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var selection BridgeSelection
			err = json.Unmarshal(raw, &selection)
			if err == nil {
				err = selection.Validate()
			}
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, tc.Valid, err)
			}
		})
	}
}
