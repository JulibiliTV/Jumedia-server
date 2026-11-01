package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type Failure struct {
	Code    int
	Message string
	Data    any
}

func (f *Failure) Error() string                        { return f.Message }
func Fail(code int, message string) error               { return FailData(code, message, nil) }
func FailData(code int, message string, data any) error { return &Failure{code, message, data} }

type result struct{ v any }
type root struct{ v map[string]any }
type raw struct{ v any }
type textResponse struct {
	body        string
	status      int
	contentType string
}
type bytesResponse struct {
	body        []byte
	contentType string
}

func Result(v any) any          { return result{v} }
func Root(v map[string]any) any { return root{v} }
func Raw(v any) any             { return raw{v} }
func Text(body string, status int, contentType string) any {
	return textResponse{body, status, contentType}
}
func Bytes(body []byte, contentType string) any { return bytesResponse{body, contentType} }
func writeResponse(w http.ResponseWriter, value any, err error, origins ...string) {
	var body any
	if err != nil {
		var f *Failure
		if errors.As(err, &f) {
			body = map[string]any{"code": f.Code, "message": f.Message, "ttl": 1, "data": f.Data}
		} else {
			body = map[string]any{"code": -500, "message": "服务器内部错误", "ttl": 1}
		}
	} else {
		switch v := value.(type) {
		case result:
			body = map[string]any{"code": 0, "message": "success", "result": v.v}
		case root:
			body = v.v
		case raw:
			body = v.v
		case textResponse:
			w.Header().Set("Content-Type", v.contentType)
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(v.status)
			_, _ = w.Write([]byte(v.body))
			return
		case bytesResponse:
			w.Header().Set("Content-Type", v.contentType)
			_, _ = w.Write(v.body)
			return
		default:
			body = map[string]any{"code": 0, "message": "0", "ttl": 1, "data": value}
		}
	}
	encoded, e := json.Marshal(body)
	if e == nil && len(origins) > 0 {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if e = decoder.Decode(&value); e == nil {
			encoded, e = json.Marshal(absoluteResources(value, origins[0]))
		}
	}
	if e != nil {
		encoded = []byte(`{"code":-500,"message":"服务器内部错误","ttl":1}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// Transform JSON values, never substrings or map keys; UseNumber preserves IDs.
func absoluteResources(value any, origin string) any {
	switch v := value.(type) {
	case string:
		for _, prefix := range []string{"/upload/", "/mock/", "/media/"} {
			if strings.HasPrefix(v, prefix) {
				return origin + v
			}
		}
	case []any:
		for i, item := range v {
			v[i] = absoluteResources(item, origin)
		}
	case map[string]any:
		for k, item := range v {
			v[k] = absoluteResources(item, origin)
		}
	}
	return value
}
