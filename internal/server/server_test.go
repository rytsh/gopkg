package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rytsh/gopkg/internal/site"
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
		{name: "empty token allows request", wantStatus: http.StatusNoContent},
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

func TestFetchAuthenticationAndCrossOrigin(t *testing.T) {
	for _, test := range []struct {
		name, token, authorization, origin, fetchSite string
		status                                        int
	}{
		{name: "missing credentials", token: "secret", status: http.StatusUnauthorized},
		{name: "wrong credentials", token: "secret", authorization: "Bearer wrong", status: http.StatusUnauthorized},
		{name: "bearer", token: "secret", authorization: "Bearer secret", status: http.StatusBadRequest},
		{name: "basic", token: "secret", authorization: "Basic Z29wa2c6c2VjcmV0", status: http.StatusBadRequest},
		{name: "no token", status: http.StatusBadRequest},
		{name: "cross origin", origin: "https://evil.test", status: http.StatusForbidden},
		{name: "authenticated cross origin", token: "secret", authorization: "Bearer secret", origin: "https://evil.test", status: http.StatusForbidden},
		{name: "cross site", fetchSite: "cross-site", status: http.StatusForbidden},
		{name: "same origin", origin: "http://example.com", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://example.com/-/fetch", nil)
			r.Header.Set("Authorization", test.authorization)
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Sec-Fetch-Site", test.fetchSite)
			w := httptest.NewRecorder()
			fetchHandler(&site.Manager{}, test.token).ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.status, w.Body.String())
			}
		})
	}
}
