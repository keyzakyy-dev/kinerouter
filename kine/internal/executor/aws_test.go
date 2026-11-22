package executor

import (
	"testing"
	"time"
)

// Pinned against the aws4 reference implementation through the JS suite
// (tests/unit/aws-sigv4.test.js); identical vectors keep the two ports in lockstep.
func TestSignAWSRequest(t *testing.T) {
	date := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	creds := awsCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}

	body := "Action=ListUsers&Version=2010-05-08"
	got, err := signAWSRequest("POST", "https://iam.amazonaws.com/",
		map[string]string{
			"Content-Type":   "application/x-www-form-urlencoded; charset=utf-8",
			"Content-Length": "35",
		}, body, "us-east-1", "iam", creds, date)
	if err != nil {
		t.Fatal(err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, " +
		"SignedHeaders=content-length;content-type;host;x-amz-date, " +
		"Signature=afcae41d2eaf39c1b479fff3b5ae64d6806f975e7b6efb60038301a42c8dcb14"
	if got["Authorization"] != want {
		t.Fatalf("IAM signature\n got %s\nwant %s", got["Authorization"], want)
	}
	if got["x-amz-date"] != "20150830T123600Z" {
		t.Fatalf("x-amz-date = %q", got["x-amz-date"])
	}

	// A ":0"-suffixed model id is escaped once in the URL and again in the
	// canonical request; getting that wrong is the classic Bedrock mismatch.
	model := "us.anthropic.claude-sonnet-4-20250514-v1:0"
	bedrockBody := `{"anthropic_version":"bedrock-2023-05-31","messages":[]}`
	got, err = signAWSRequest("POST",
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/"+escapeAWSUri(model)+"/invoke-with-response-stream",
		map[string]string{"Content-Type": "application/json"}, bedrockBody,
		"us-east-1", "bedrock", creds, date)
	if err != nil {
		t.Fatal(err)
	}
	want = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/bedrock/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-date, " +
		"Signature=53d28679a943780f541a376e6ae08bb2b10379169a756a881a166d63132c94ab"
	if got["Authorization"] != want {
		t.Fatalf("bedrock signature\n got %s\nwant %s", got["Authorization"], want)
	}
}

func TestSignAWSRequestRejectsQueryAndBadRegion(t *testing.T) {
	creds := awsCredentials{AccessKeyID: "a", SecretAccessKey: "b"}
	if _, err := signAWSRequest("POST", "https://x.example/?a=1", nil, "", "us-east-1", "bedrock", creds, time.Now()); err == nil {
		t.Fatal("query string must be refused")
	}
	// The region lands in the hostname, so anything but the documented shape is refused.
	if _, err := resolveAWSRegion(Credentials{Region: "evil.com/x"}); err == nil {
		t.Fatal("bad region must be refused")
	}
	if r, err := resolveAWSRegion(Credentials{}); err != nil || r != bedrockSigningRegion {
		t.Fatalf("default region = %q, %v", r, err)
	}
}

func TestParseAWSIni(t *testing.T) {
	body := "[default]\naccess_key_id = AKIA\n[profile sso]\nsso_session = x\n[other]\naccess_key_id = zz\n"
	if v := parseAWSIni(body, "default"); v["access_key_id"] != "AKIA" {
		t.Fatalf("default = %v", v)
	}
	if v := parseAWSIni(body, "sso"); v["sso_session"] != "x" {
		t.Fatalf("profile prefix not stripped: %v", v)
	}
	if v := parseAWSIni(body, "missing"); len(v) != 0 {
		t.Fatalf("missing profile = %v", v)
	}
}
