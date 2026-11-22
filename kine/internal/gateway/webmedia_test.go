package gateway

import "testing"

func TestUcAspect(t *testing.T) {
	if got := ucAspect("9:16"); got["aspect_ratio"] != "9:16" || got["imageWidth"] != "576" {
		t.Fatalf("direct aspect = %v", got)
	}
	// 1024x1280 is closer to 4:5 than to 3:4 or 1:1.
	if got := ucAspect("1024x1280"); got["aspect_ratio"] != "4:5" {
		t.Fatalf("nearest aspect = %v", got)
	}
	if got := ucAspect(""); got["aspect_ratio"] != "1:1" {
		t.Fatalf("fallback aspect = %v", got)
	}
}

func TestVeoAspect(t *testing.T) {
	cases := map[string]string{
		"1080x1920 portrait": "VIDEO_ASPECT_RATIO_PORTRAIT",
		"1:1 square":         "VIDEO_ASPECT_RATIO_SQUARE",
		"16:9":               "VIDEO_ASPECT_RATIO_LANDSCAPE",
		"":                   "VIDEO_ASPECT_RATIO_LANDSCAPE",
	}
	for in, want := range cases {
		if got := veoAspect(in, "VIDEO_ASPECT_RATIO"); got != want {
			t.Fatalf("veoAspect(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCollectImageRefs(t *testing.T) {
	got := collectImageRefs(map[string]any{
		"image": "a", "images": []any{"b", 1, "c"},
	})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("refs = %v", got)
	}
}

// ucJWTClaim must read a claim without verifying the signature: the token was
// just minted by Clerk over TLS, so a second verification adds nothing here.
func TestUcJWTClaim(t *testing.T) {
	// {"uid":"user_1","sid":"sess_1"}
	tok := "e30.eyJ1aWQiOiJ1c2VyXzEiLCJzaWQiOiJzZXNzXzEifQ.sig"
	if got := ucJWTClaim(tok, "uid"); got != "user_1" {
		t.Fatalf("uid = %q", got)
	}
	if got := ucJWTClaim("garbage", "uid"); got != "" {
		t.Fatalf("garbage token must yield no claim, got %q", got)
	}
}

func TestCookieJar(t *testing.T) {
	j := newJar("__client=abc; other=1")
	j.mu.Lock()
	j.vals["__client"] = "xyz"
	j.mu.Unlock()
	if j.get("__client") != "xyz" {
		t.Fatalf("jar value = %q", j.get("__client"))
	}
	if newJar("").header() != "" {
		t.Fatal("empty jar must produce no header")
	}
}
