package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (fn fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestGetRepoBranchesPagination(t *testing.T) {
	previousClient := httpClient
	defer func() { httpClient = previousClient }()
	pages := []int{}
	httpClient = &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.github.com" || req.URL.Path != "/repos/Kuadrant/kuadrant-operator/branches" {
			t.Fatalf("unexpected endpoint: %s", req.URL)
		}
		if req.URL.Query().Get("per_page") != "100" {
			t.Fatal("expected 100 results per page")
		}
		page := req.URL.Query().Get("page")
		var entries []struct {
			Name string `json:"name"`
		}
		switch page {
		case "1":
			pages = append(pages, 1)
			for i := 0; i < 100; i++ {
				entries = append(entries, struct {
					Name string `json:"name"`
				}{Name: fmt.Sprintf("branch-%03d", i)})
			}
		case "2":
			pages = append(pages, 2)
			entries = append(entries, struct {
				Name string `json:"name"`
			}{Name: "release-1.6"})
		default:
			t.Fatalf("unexpected page %s", page)
		}
		data, _ := json.Marshal(entries)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})}
	got, err := getRepoBranches("Kuadrant/kuadrant-operator")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 101 || got[100] != "release-1.6" {
		t.Fatalf("branches = %v", got)
	}
	if len(pages) != 2 {
		t.Fatalf("visited pages %v, want [1 2]", pages)
	}
}

func TestGetRepoBranchesError(t *testing.T) {
	previousClient := httpClient
	defer func() { httpClient = previousClient }()
	httpClient = &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("rate limited")), Header: make(http.Header)}, nil
	})}
	_, err := getRepoBranches("Kuadrant/kuadrant-operator")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("error = %v", err)
	}
}
