package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFileInputStreamsExactlyTheHandedOverFile(t *testing.T) {
	served := "hello"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, served) }))
	defer server.Close()
	config := map[string]interface{}{"file": map[string]interface{}{"name": "a.txt", "path": "docs/a.txt", "mediaType": "text/plain", "sizeBytes": 5, "url": server.URL + "/a"}}
	file, err := FileInput(config, "file")
	if err != nil || file.Path != "docs/a.txt" || file.SizeBytes != 5 {
		t.Fatalf("file = %#v, %v", file, err)
	}
	for body, ok := range map[string]bool{"hello": true, "hell": false, "hello!": false} {
		served = body
		reader, err := file.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if (err == nil) != ok || (ok && string(data) != "hello") {
			t.Fatalf("served %q: read %q, %v", body, data, err)
		}
	}
	for _, value := range []interface{}{"docs/a.txt", map[string]interface{}{"name": "a", "url": "file:///etc/passwd", "sizeBytes": 1}, map[string]interface{}{"name": "a", "url": server.URL, "sizeBytes": MaxFileBytes + 1}} {
		if _, err := FileInput(map[string]interface{}{"file": value}, "file"); err == nil {
			t.Fatalf("accepted %#v", value)
		}
	}
}
