package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAdmin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	tests := []struct {
		name       string
		token      string
		authorize  func(*http.Request)
		wantStatus int
	}{
		{name: "disabled", wantStatus: http.StatusNotFound},
		{name: "missing credentials", token: "secret", wantStatus: http.StatusUnauthorized},
		{name: "wrong basic password", token: "secret", authorize: func(r *http.Request) { r.SetBasicAuth("gopkg", "wrong") }, wantStatus: http.StatusUnauthorized},
		{name: "basic password", token: "secret", authorize: func(r *http.Request) { r.SetBasicAuth("gopkg", "secret") }, wantStatus: http.StatusNoContent},
		{name: "bearer token", token: "secret", authorize: func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }, wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
			if test.authorize != nil {
				test.authorize(request)
			}
			response := httptest.NewRecorder()
			requireAdmin(test.token, next).ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
