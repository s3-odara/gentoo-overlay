package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreatePullRequest_Success(t *testing.T) {
	var gotAuth string
	var gotBody PullRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/pulls" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": "https://github.com/owner/repo/pull/1"})
	}))
	defer server.Close()

	client := NewClient("gh-token", server.Client())
	client.BaseURL = server.URL

	url, err := client.CreatePullRequest(context.Background(), "owner", "repo", PullRequest{
		Title: "Update gui-apps/fuzzel",
		Head:  "update/gui-apps-fuzzel-abc123",
		Base:  "main",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/owner/repo/pull/1" {
		t.Fatalf("unexpected url: %s", url)
	}
	if gotAuth != "Bearer gh-token" {
		t.Fatalf("expected Bearer token, got %q", gotAuth)
	}
	if gotBody.Title != "Update gui-apps/fuzzel" {
		t.Fatalf("unexpected title: %q", gotBody.Title)
	}
}

func TestCreatePullRequest_ForbiddenMentionsPermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer server.Close()

	client := NewClient("gh-token", server.Client())
	client.BaseURL = server.URL

	_, err := client.CreatePullRequest(context.Background(), "owner", "repo", PullRequest{Title: "t", Head: "h", Base: "b", Body: "b"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pull-requests: write") {
		t.Fatalf("error should mention pull-requests: write, got: %v", err)
	}
}

func TestCreatePullRequest_MissingToken(t *testing.T) {
	client := NewClient("", http.DefaultClient)
	_, err := client.CreatePullRequest(context.Background(), "owner", "repo", PullRequest{Title: "t", Head: "h", Base: "b", Body: "b"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("error should mention missing token, got: %v", err)
	}
}
