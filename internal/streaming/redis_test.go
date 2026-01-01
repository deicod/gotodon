package streaming

import (
	"testing"
)

func TestExtractStreamFromChannel(t *testing.T) {
	tests := []struct {
		channel string
		prefix  string
		want    string
	}{
		{"gotodon:stream:public", "gotodon", "public"},
		{"gotodon:stream:user", "gotodon", "user"},
		{"gotodon:stream:hashtag:golang", "gotodon", "hashtag:golang"},
		{"gotodon:stream:user:12345", "gotodon", "user:12345"},
		{"gotodon:stream:user:notification:12345", "gotodon", "user:notification:12345"},
		{"gotodon:stream:direct:42", "gotodon", "direct:42"},
		{"prefix:stream:list:1", "prefix", "list:1"},
	}

	for _, tt := range tests {
		got := extractStreamFromChannel(tt.channel, tt.prefix)
		if got != tt.want {
			t.Errorf("extractStreamFromChannel(%q, %q) = %q, want %q", tt.channel, tt.prefix, got, tt.want)
		}
	}
}

func TestTimelineKeys(t *testing.T) {
	// Test home timeline key
	home := HomeTimelineKey(12345)
	if home != "home:12345" {
		t.Errorf("HomeTimelineKey(12345) = %q, want %q", home, "home:12345")
	}

	// Test public timeline key
	public := PublicTimelineKey()
	if public != "public" {
		t.Errorf("PublicTimelineKey() = %q, want %q", public, "public")
	}

	// Test list timeline key
	list := ListTimelineKey(999)
	if list != "list:999" {
		t.Errorf("ListTimelineKey(999) = %q, want %q", list, "list:999")
	}
}
