package streaming

import (
	"net/url"
	"reflect"
	"testing"
)

func TestResolveStreamSubscriptions_PathPublic(t *testing.T) {
	subs, err := resolveStreamSubscriptions("/api/v1/streaming/public", url.Values{}, StreamPublic)
	if err != nil {
		t.Fatalf("resolveStreamSubscriptions error = %v", err)
	}
	want := []string{StreamPublic}
	if !reflect.DeepEqual(subs, want) {
		t.Fatalf("subscriptions = %#v, want %#v", subs, want)
	}
}

func TestResolveStreamSubscriptions_PathHashtag(t *testing.T) {
	values := url.Values{"tag": []string{"golang"}}
	subs, err := resolveStreamSubscriptions("/api/v1/streaming/hashtag", values, StreamPublic)
	if err != nil {
		t.Fatalf("resolveStreamSubscriptions error = %v", err)
	}
	want := []string{StreamHashtag + ":golang"}
	if !reflect.DeepEqual(subs, want) {
		t.Fatalf("subscriptions = %#v, want %#v", subs, want)
	}
}

func TestResolveStreamSubscriptions_QueryMultiStream(t *testing.T) {
	values := url.Values{
		"stream": []string{StreamHashtag, StreamHashtagLocal, StreamList},
		"tag":    []string{"go", "rust"},
		"list":   []string{"42"},
	}
	subs, err := resolveStreamSubscriptions("/api/v1/streaming", values, "")
	if err != nil {
		t.Fatalf("resolveStreamSubscriptions error = %v", err)
	}
	want := []string{StreamHashtag + ":go", StreamHashtagLocal + ":rust", StreamList + ":42"}
	if !reflect.DeepEqual(subs, want) {
		t.Fatalf("subscriptions = %#v, want %#v", subs, want)
	}
}

func TestResolveStreamSubscriptions_DefaultPublic(t *testing.T) {
	subs, err := resolveStreamSubscriptions("/api/v1/streaming", url.Values{}, StreamPublic)
	if err != nil {
		t.Fatalf("resolveStreamSubscriptions error = %v", err)
	}
	want := []string{StreamPublic}
	if !reflect.DeepEqual(subs, want) {
		t.Fatalf("subscriptions = %#v, want %#v", subs, want)
	}
}

func TestResolveStreamSubscriptions_MissingTag(t *testing.T) {
	_, err := resolveStreamSubscriptions("/api/v1/streaming", url.Values{"stream": []string{StreamHashtag}}, "")
	if err == nil {
		t.Fatal("expected error for missing tag")
	}
}

func TestResolveStreamSubscriptions_UnknownStream(t *testing.T) {
	_, err := resolveStreamSubscriptions("/api/v1/streaming", url.Values{"stream": []string{"unknown"}}, "")
	if err == nil {
		t.Fatal("expected error for unknown stream")
	}
}
