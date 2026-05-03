package convert

import "testing"

// Verifies that repeated HOST/SERVER metadata entries become an ordered
// `servers` array and that the optional " - description" suffix is parsed
// out into Server.Description.
func TestServers_MultipleHostMetadata(t *testing.T) {
	refract := []byte(`{
		"element": "parseResult",
		"content": [{
			"element": "category",
			"meta": {"title": {"element":"string","content":"API"}},
			"attributes": {"metadata": {"element":"array","content": [
				{"element":"member","content":{"key":{"element":"string","content":"HOST"},"value":{"element":"string","content":"https://api.example.com"}}},
				{"element":"member","content":{"key":{"element":"string","content":"HOST"},"value":{"element":"string","content":"https://staging.example.com - Staging"}}},
				{"element":"member","content":{"key":{"element":"string","content":"SERVER"},"value":{"element":"string","content":"https://sandbox.example.com - Sandbox env"}}}
			]}},
			"content": []
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(doc.Servers), 3; got != want {
		t.Fatalf("got %d servers, want %d: %+v", got, want, doc.Servers)
	}
	cases := []struct{ url, desc string }{
		{"https://api.example.com", ""},
		{"https://staging.example.com", "Staging"},
		{"https://sandbox.example.com", "Sandbox env"},
	}
	for i, c := range cases {
		if doc.Servers[i].URL != c.url {
			t.Errorf("servers[%d].URL = %q, want %q", i, doc.Servers[i].URL, c.url)
		}
		if doc.Servers[i].Description != c.desc {
			t.Errorf("servers[%d].Description = %q, want %q", i, doc.Servers[i].Description, c.desc)
		}
	}
}

// A single HOST: with no description must keep working (back-compat).
func TestServers_SingleHostBackCompat(t *testing.T) {
	refract := []byte(`{
		"element": "parseResult",
		"content": [{
			"element": "category",
			"meta": {"title": {"element":"string","content":"API"}},
			"attributes": {"metadata": {"element":"array","content": [
				{"element":"member","content":{"key":{"element":"string","content":"HOST"},"value":{"element":"string","content":"https://api.example.com"}}}
			]}},
			"content": []
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "https://api.example.com" || doc.Servers[0].Description != "" {
		t.Fatalf("unexpected servers: %+v", doc.Servers)
	}
}
