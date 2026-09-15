package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCombinedHTTPHandlerRoutesAPIAndWebUIPaths(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	webui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	handler := combinedHTTPHandler(api, webui)

	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/healthz", want: http.StatusNoContent},
		{path: "/v1/models", want: http.StatusNoContent},
		{path: "/", want: http.StatusCreated},
		{path: "/api/auth/login", want: http.StatusCreated},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want {
			t.Fatalf("path %s: status = %d, want %d", test.path, recorder.Code, test.want)
		}
	}
}
