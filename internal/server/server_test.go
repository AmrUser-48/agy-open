package server

import (
	"net/http/httptest"
	"testing"
)

func TestSameOriginRequest(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "no origin for local client", host: "127.0.0.1:8080", want: true},
		{name: "same http origin", origin: "http://127.0.0.1:8080", host: "127.0.0.1:8080", want: true},
		{name: "same https origin", origin: "https://agy.example.test", host: "agy.example.test", want: true},
		{name: "cross origin", origin: "https://evil.example", host: "127.0.0.1:8080", want: false},
		{name: "different port", origin: "http://127.0.0.1:9000", host: "127.0.0.1:8080", want: false},
		{name: "null origin", origin: "null", host: "127.0.0.1:8080", want: false},
		{name: "origin with path", origin: "http://127.0.0.1:8080/evil", host: "127.0.0.1:8080", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tt.host+"/api/approval/respond", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := sameOriginRequest(r); got != tt.want {
				t.Fatalf("sameOriginRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
