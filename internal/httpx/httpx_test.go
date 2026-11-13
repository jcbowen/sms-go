package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDoSuccess 验证正常响应体读取。
func TestDoSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Code":"OK"}`))
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := Do(context.Background(), server.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"Code":"OK"}` {
		t.Fatalf("响应体异常: %s", body)
	}
}

// TestDoHTTPError 验证非 2xx 返回 HTTPError。
func TestDoHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("上游故障"))
	}))
	defer server.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	_, err := Do(context.Background(), server.Client(), req)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("期望 HTTPError(502)，实际 %v", err)
	}
	if !strings.Contains(httpErr.Error(), "上游故障") {
		t.Fatalf("错误信息未携带响应片段: %s", httpErr.Error())
	}
}

// TestDoOversize 验证响应体超限保护。
func TestDoOversize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, MaxResponseSize+1))
	}))
	defer server.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if _, err := Do(context.Background(), server.Client(), req); err == nil {
		t.Fatal("超限响应体应返回错误")
	}
}
