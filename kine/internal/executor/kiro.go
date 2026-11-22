package executor

// Kiro / AWS CodeWhisperer transport quirks. Ported from
// open-sse/executors/kiro.js buildHeaders + getOrderedBaseUrls:
//
//   - api_key is sent as a bearer token with TokenType: API_KEY; external_idp
//     gets TokenType: EXTERNAL_IDP. Both, plus idc, must ride the Amazon
//     surfaces — the kiro.dev gateway 403s their tokens.
//   - codewhisperer.* is the only surface that wants X-Amz-Target.
//   - the region from the connection is spliced into amazonaws.com hosts, and
//     only when it is one of the three covered services' regions (the region
//     reaches a hostname, so it is validated rather than trusted).

import (
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

const kiroCodewhispererTarget = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"

var kiroServiceRegions = map[string]map[string]bool{
	"q":             {"us-east-1": true, "eu-central-1": true},
	"runtime":       {"us-east-1": true, "eu-central-1": true},
	"codewhisperer": {"us-east-1": true},
}

// kiroURL picks the endpoint for the connection's auth method.
//
// ponytail: one surface per attempt. BaseExecutor walks kiro.dev →
// codewhisperer → q on 429/5xx/401/403; the Go gateway only does account
// fallback. Add surface rotation when a single kiro account must survive an
// edge outage.
func kiroURL(current string, t *providerdata.Transport) string {
	if t == nil {
		return current
	}
	urls := append([]string{}, t.BaseURLs...)
	if len(urls) == 0 && t.BaseURL != "" {
		urls = []string{t.BaseURL}
	}
	if len(urls) == 0 {
		return current
	}
	var amazon, others []string
	for _, u := range urls {
		if strings.Contains(u, "amazonaws.com") {
			amazon = append(amazon, u)
		} else {
			others = append(others, u)
		}
	}
	sort.SliceStable(amazon, func(i, j int) bool {
		return strings.Contains(amazon[i], "://q.") && !strings.Contains(amazon[j], "://q.")
	})
	if len(amazon) > 0 {
		return amazon[0]
	}
	if len(others) > 0 {
		return others[0]
	}
	return current
}

// kiroHeaders builds the credential + marker headers for the chosen surface.
func kiroHeaders(creds Credentials, endpoint string) map[string]string {
	h := map[string]string{
		"Amz-Sdk-Request":                 "attempt=1; max=3",
		"Amz-Sdk-Invocation-Id":           uuid.NewString(),
		"x-amzn-kiro-agent-mode":          "spec",
		"x-amzn-codewhisperer-machine-id": "kiro-desktop",
	}
	if strings.Contains(endpoint, "://codewhisperer.") {
		h["X-Amz-Target"] = kiroCodewhispererTarget
	}
	authMethod := creds.AuthMethod
	isAPIKey := authMethod == "api_key"
	token := creds.AccessToken
	if isAPIKey && creds.APIKey != "" {
		token = creds.APIKey
	}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	// SSO bearer mirrors the OAuth access token only; API keys are not SSO tokens.
	if creds.AccessToken != "" {
		h["x-amz-sso-bearer"] = creds.AccessToken
	}
	if isAPIKey {
		h["TokenType"] = "API_KEY"
	} else if authMethod == "external_idp" {
		h["TokenType"] = "EXTERNAL_IDP"
	}
	if arn := creds.ProfileARN; arn != "" {
		h["x-amzn-codewhisperer-profile-arn"] = arn
	}
	return h
}

// kiroRegionalize rewrites an amazonaws.com host into the connection's region
// when that service covers it. Validation: region must match [a-z0-9-] and be
// in the service's covered set, so a crafted region cannot retarget the host.
func kiroRegionalize(endpoint, region string) string {
	if region == "" || !coveredRegion(region) {
		return endpoint
	}
	for service, covered := range kiroServiceRegions {
		host := "://" + service + "."
		if !strings.Contains(endpoint, host) || !covered[region] {
			continue
		}
		u, err := url.Parse(endpoint)
		if err != nil || !strings.HasSuffix(u.Hostname(), ".amazonaws.com") {
			return endpoint
		}
		u.Host = service + "." + region + ".amazonaws.com"
		return u.String()
	}
	return endpoint
}

func coveredRegion(region string) bool {
	for i := 0; i < len(region); i++ {
		c := region[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' {
			return false
		}
	}
	return region != ""
}
