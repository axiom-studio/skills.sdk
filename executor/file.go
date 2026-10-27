package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// FileType is the input schema type of an action input that takes a file
// from the chat the action runs in, written {"type": "file"} on a top-level
// input property. The model passes the file's path; the host hands the skill
// a File in its place.
const FileType = "file"

// MaxFileBytes is the largest file a host hands a skill.
const MaxFileBytes = 100 << 20

// File is a chat file a host handed this skill for a file input. Only its
// description travels in the call; Open streams the bytes from URL, which
// serves this one file for a short while.
type File struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	URL       string `json:"url"`
}

// FileInput is the file a host handed the skill for the file input key.
func FileInput(config map[string]interface{}, key string) (File, error) {
	var file File
	encoded, err := json.Marshal(config[key])
	if err != nil || json.Unmarshal(encoded, &file) != nil || file.Name == "" || file.URL == "" || file.SizeBytes < 0 {
		return File{}, fmt.Errorf("%s must be a file from the chat", key)
	}
	if file.SizeBytes > MaxFileBytes {
		return File{}, fmt.Errorf("%s is larger than %d MB", file.Name, MaxFileBytes>>20)
	}
	link, err := url.Parse(file.URL)
	if err != nil || (link.Scheme != "https" && link.Scheme != "http") || link.Host == "" {
		return File{}, fmt.Errorf("%s has no link to read it from", file.Name)
	}
	return file, nil
}

// FileClient fetches handed-over files. It never follows redirects; the
// caller's context bounds how long a transfer may take.
var FileClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Open streams the file's bytes. The reader fails rather than return more or
// fewer than SizeBytes.
func (f File) Open(ctx context.Context) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not read %s from the chat", f.Name)
	}
	response, err := FileClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("could not read %s from the chat", f.Name)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("could not read %s from the chat: its link answered %d", f.Name, response.StatusCode)
	}
	return &exactReader{body: response.Body, remaining: f.SizeBytes}, nil
}

var errFileSize = errors.New("the file's size changed while it was read")

type exactReader struct {
	body      io.ReadCloser
	remaining int64
}

func (r *exactReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var extra [1]byte
		if n, _ := r.body.Read(extra[:]); n > 0 {
			return 0, errFileSize
		}
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.body.Read(p)
	r.remaining -= int64(n)
	if err == io.EOF && r.remaining > 0 {
		return n, errFileSize
	}
	if err == io.EOF {
		err = nil
	}
	return n, err
}

func (r *exactReader) Close() error { return r.body.Close() }
