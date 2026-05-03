package convert

import (
	"strings"
	"testing"
)

// Resource group prose (everything written under `# Group <Name>` before
// the first resource) must surface as the OAS `tags[].description` so it
// renders alongside the group in docs viewers.
func TestTagDescription_FromGroupCopy(t *testing.T) {
	refract := []byte(`{
		"element": "parseResult",
		"content": [{
			"element": "category",
			"meta": {"title": {"element":"string","content":"API"}},
			"content": [{
				"element": "category",
				"meta": {
					"title": {"element":"string","content":"Uploads v2"},
					"classes": {"element":"array","content":[{"element":"string","content":"resourceGroup"}]}
				},
				"content": [
					{"element":"copy","content":"Base path: ` + "`" + `/v2/uploads` + "`" + `."},
					{"element":"copy","content":"Endpoints for handing the API a raw media file."},
					{"element":"resource","attributes":{"href":{"element":"string","content":"/v2/uploads/presign"}},"content":[{
						"element":"transition","content":[{"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]}
						]}]
					}]}
				]
			}]
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tags) != 1 {
		t.Fatalf("got %d tags, want 1: %+v", len(doc.Tags), doc.Tags)
	}
	tag := doc.Tags[0]
	if tag.Name != "Uploads v2" {
		t.Errorf("tag name = %q", tag.Name)
	}
	if !strings.Contains(tag.Description, "Base path") || !strings.Contains(tag.Description, "raw media file") {
		t.Errorf("tag description missing expected prose; got:\n%s", tag.Description)
	}
	// Multi-paragraph prose must be joined with a blank line.
	if !strings.Contains(tag.Description, "\n\n") {
		t.Errorf("expected paragraphs to be separated by blank line; got:\n%s", tag.Description)
	}
}
