package api

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// download streams a non-JSON response (CSV, PDF, JSON bundle) into w and
// returns the filename the server suggested, falling back to the last path
// segment. A non-2xx body is the usual JSON error envelope.
func (c *Client) download(ctx context.Context, r *request, w io.Writer) (string, error) {
	resp, err := c.stream(ctx, r)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", newAPIError(resp, body)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return "", err
	}
	return filenameFrom(resp, r.path), nil
}

// downloadBytes is download for small non-JSON bodies (the OpenAPI spec).
func (c *Client) downloadBytes(ctx context.Context, r *request) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := c.download(ctx, r, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// filenameFrom reads the filename parameter of Content-Disposition.
func filenameFrom(resp *http.Response, path string) string {
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		if name := params["filename"]; name != "" {
			return name
		}
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// setQueryList sets a parameter from a list, comma-joined: the API treats the
// tags filter as a comma-separated set (array overlap), not repeated keys.
func setQueryList(r *request, key string, values []string) *request {
	filtered := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			filtered = append(filtered, v)
		}
	}
	if len(filtered) == 0 {
		return r
	}
	return r.setQueryValue(key, strings.Join(filtered, ","))
}

// setQueryPointerBool sets a tri-state flag: nil leaves it out entirely,
// otherwise the literal "true"/"false" is sent, because the API distinguishes
// "linked=false" from an absent filter.
func setQueryPointerBool(r *request, key string, value *bool) *request {
	if value == nil {
		return r
	}
	if *value {
		return r.setQueryValue(key, "true")
	}
	return r.setQueryValue(key, "false")
}

// pathEscape escapes one path segment (an identifier or a sentinel such as
// "uncategorized").
func pathEscape(segment string) string { return url.PathEscape(segment) }
