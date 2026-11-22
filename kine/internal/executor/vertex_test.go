package executor

import "testing"

// The four credential shapes must each yield the endpoint the JS executor
// builds; these are copied from open-sse/executors/vertex.js buildUrl.
func TestVertexURL(t *testing.T) {
	region := vertexCreds{accessToken: "ya29.tok", projectID: "proj", location: "us-central1"}
	if got := vertexURL(false, "gemini-2.5-flash", region, true); got !=
		"https://aiplatform.googleapis.com/v1/projects/proj/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse" {
		t.Fatalf("oauth stream url = %s", got)
	}
	raw := vertexCreds{rawKey: "AIza key", location: "global"}
	if got := vertexURL(false, "gemini-2.5-flash", raw, false); got !=
		"https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-flash:generateContent?key=AIza+key" {
		t.Fatalf("raw key url = %s", got)
	}
	if got := vertexURL(false, "gemini-2.5-flash", vertexCreds{projectID: "proj", location: "global", rawKey: "k"}, true); got !=
		"https://aiplatform.googleapis.com/v1/projects/proj/locations/global/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse&key=k" {
		t.Fatalf("projected raw key url = %s", got)
	}
	partner := vertexCreds{projectID: "proj", rawKey: "k"}
	if got := vertexURL(true, "qwen/qwen3-next-80b-a3b-instruct-maas", partner, true); got !=
		"https://aiplatform.googleapis.com/v1/projects/proj/locations/global/endpoints/openapi/chat/completions?key=k" {
		t.Fatalf("partner url = %s", got)
	}
}

func TestVertexCredentialShapes(t *testing.T) {
	if _, ok := vertexWire("vertex-partner"); !ok {
		t.Fatal("vertex-partner must dispatch here")
	}
	if _, ok := vertexWire("gemini"); ok {
		t.Fatal("plain gemini must not dispatch here")
	}
	if blob := vertexJSONBlob(Credentials{APIKey: `{"type":"authorized_user","refresh_token":"r"}`}); blob["refresh_token"] != "r" {
		t.Fatalf("ADC blob not parsed: %v", blob)
	}
	if blob := vertexJSONBlob(Credentials{APIKey: "AIzaSy"}); blob != nil {
		t.Fatal("a raw key must not parse as JSON")
	}
}
