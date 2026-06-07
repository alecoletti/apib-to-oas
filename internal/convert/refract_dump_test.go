package convert_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/drafter"
)

func TestDumpStatusErrorRefract(t *testing.T) {
	apib := "FORMAT: 1A\n\n# Test API\n\n## Data Structures\n\n### StatusError (object)\n\nStandard error response.\n\n+ error (object, required)\n    + errCode: invalid_request (string, required) - Machine-readable error code\n    + message: Something went wrong (string, required) - Human-readable description\n"

	runner, err := drafter.New()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := runner.Parse(context.Background(), []byte(apib))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := findNodeByID(doc, "StatusError")
	b, _ := json.MarshalIndent(found, "", "  ")
	fmt.Println(string(b))
}

func findNodeByID(node any, id string) any {
	switch v := node.(type) {
	case map[string]any:
		if meta, ok := v["meta"].(map[string]any); ok {
			if idObj, ok := meta["id"].(map[string]any); ok {
				if idObj["content"] == id {
					return v
				}
			}
		}
		for _, child := range v {
			if r := findNodeByID(child, id); r != nil {
				return r
			}
		}
	case []any:
		for _, item := range v {
			if r := findNodeByID(item, id); r != nil {
				return r
			}
		}
	}
	return nil
}
