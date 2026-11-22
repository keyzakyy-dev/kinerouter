package executor

// AWS Signature Version 4 for Bedrock. Ported from open-sse/utils/awsSigv4.js and
// open-sse/shared/awsCredentials.js.
//
// Hand-rolled rather than pulling aws-sdk-go-v2: every request signed here is a
// single query-less POST, the case SigV4 makes hard (multi-value headers, query
// ordering) never arises, and the SDK would add ~10MB for one provider.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	awsAlgorithm  = "AWS4-HMAC-SHA256"
	awsTerminator = "aws4_request"
	awsKeyPrefix  = "AWS4"
	// bedrockSigningRegion is used when the connection names no region.
	bedrockSigningRegion = "us-east-1"
)

var (
	awsRegionPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	awsProfilePattern = regexp.MustCompile(`^[\w@+.%: -]{1,64}$`)
)

// awsCredentials is one resolved AWS account.
type awsCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

// escapeAWSUri percent-encodes per RFC 3986, leaving only the unreserved set.
// net/url.PathEscape leaves characters AWS escapes (and escapes some it does not);
// escaping every byte keeps the canonical request and the wire path identical.
func escapeAWSUri(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// canonicalAWSHeaders builds the canonical header block and its SignedHeaders list.
func canonicalAWSHeaders(headers map[string]string) (string, string) {
	names := make([]string, 0, len(headers))
	values := make(map[string]string, len(headers))
	for k, v := range headers {
		name := strings.ToLower(k)
		names = append(names, name)
		values[name] = v
	}
	sort.Strings(names)
	var block strings.Builder
	for _, name := range names {
		value := strings.Join(strings.Fields(values[name]), " ")
		block.WriteString(name)
		block.WriteByte(':')
		block.WriteString(value)
		block.WriteByte('\n')
	}
	return block.String(), strings.Join(names, ";")
}

// signAWSRequest signs one request and returns every header to send with it.
// The raw path must already be escaped once; this escapes each segment again for
// the canonical request, which is what a ":0"-suffixed Bedrock model id needs.
func signAWSRequest(method, rawURL string, headers map[string]string, body string,
	region, service string, creds awsCredentials, date time.Time) (map[string]string, error) {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return nil, fmt.Errorf("AWS SigV4 requires both accessKeyId and secretAccessKey")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.RawQuery != "" {
		return nil, fmt.Errorf("AWS SigV4 signing does not support query strings (got %q)", parsed.RawQuery)
	}
	amzDate := date.UTC().Format("20060102T150405Z")
	dateStamp := amzDate[:8]

	toSign := map[string]string{}
	for k, v := range headers {
		toSign[k] = v
	}
	toSign["host"] = parsed.Host
	toSign["x-amz-date"] = amzDate
	if creds.SessionToken != "" {
		toSign["x-amz-security-token"] = creds.SessionToken
	}
	canonicalHeaders, signedHeaders := canonicalAWSHeaders(toSign)

	segments := strings.Split(parsed.EscapedPath(), "/")
	for i, s := range segments {
		segments[i] = escapeAWSUri(s)
	}
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(method),
		strings.Join(segments, "/"),
		"",
		canonicalHeaders,
		signedHeaders,
		sha256Hex(body),
	}, "\n")

	scope := dateStamp + "/" + region + "/" + service + "/" + awsTerminator
	stringToSign := strings.Join([]string{
		awsAlgorithm, amzDate, scope, sha256Hex(canonicalRequest),
	}, "\n")

	signingKey := hmacSHA256([]byte(awsKeyPrefix+creds.SecretAccessKey), dateStamp)
	signingKey = hmacSHA256(signingKey, region)
	signingKey = hmacSHA256(signingKey, service)
	signingKey = hmacSHA256(signingKey, awsTerminator)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	out := map[string]string{}
	for k, v := range toSign {
		if k == "host" {
			continue // net/http derives it from the URL
		}
		out[k] = v
	}
	out["Authorization"] = awsAlgorithm + " Credential=" + creds.AccessKeyID + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	return out, nil
}

// resolveAWSCredentials reads the Bedrock credential form the connection uses.
// A named profile wins over static keys, since a user who filled one in meant it.
func resolveAWSCredentials(c Credentials) (awsCredentials, error) {
	region, err := resolveAWSRegion(c)
	if err != nil {
		return awsCredentials{}, err
	}
	profile, _ := c.Extra["profile"].(string)
	if profile != "" {
		key, secret, token, err := readAWSProfile(profile)
		if err != nil {
			return awsCredentials{}, err
		}
		return awsCredentials{AccessKeyID: key, SecretAccessKey: secret, SessionToken: token, Region: region}, nil
	}
	keyID, _ := c.Extra["accessKeyId"].(string)
	token, _ := c.Extra["sessionToken"].(string)
	if keyID == "" || c.APIKey == "" {
		return awsCredentials{}, fmt.Errorf("Bedrock static credentials are incomplete. Set the AWS secret " +
			"access key as the provider API key and providerSpecificData.accessKeyId to the AWS access " +
			"key id, or set providerSpecificData.profile to use a local AWS profile instead.")
	}
	if strings.HasPrefix(keyID, "ASIA") && token == "" {
		return awsCredentials{}, fmt.Errorf("this looks like a temporary AWS access key (ASIA…) but no " +
			"sessionToken was provided. Add providerSpecificData.sessionToken, or use " +
			"providerSpecificData.profile so credentials are resolved automatically.")
	}
	return awsCredentials{AccessKeyID: keyID, SecretAccessKey: c.APIKey, SessionToken: token, Region: region}, nil
}

// resolveAWSRegion validates the region, which is interpolated into the request
// hostname: an unvalidated value here would let a connection redirect signed
// traffic — and its session token — to an attacker-chosen origin.
func resolveAWSRegion(c Credentials) (string, error) {
	candidates := []struct{ value, source string }{
		{c.Region, "providerSpecificData.region"},
		{os.Getenv("AWS_REGION"), "the AWS_REGION environment variable"},
		{os.Getenv("AWS_DEFAULT_REGION"), "the AWS_DEFAULT_REGION environment variable"},
		{bedrockSigningRegion, "the built-in default"},
	}
	for _, cand := range candidates {
		region := strings.TrimSpace(cand.value)
		if region == "" {
			continue
		}
		if !awsRegionPattern.MatchString(region) {
			return "", fmt.Errorf("invalid AWS region %q from %s: a region looks like \"us-east-1\" "+
				"(lowercase letters, digits and hyphens only) because it builds the Bedrock hostname", cand.value, cand.source)
		}
		return region, nil
	}
	return bedrockSigningRegion, nil
}

// readAWSProfile reads one profile from ~/.aws/credentials, then ~/.aws/config.
// ponytail: static profile keys only — `sso_session` profiles need the SSO token
// cache and a GetRoleCredentials call. Run `aws configure export-credentials` or
// switch the connection to static keys for SSO; add the SSO flow when a Bedrock
// SSO user reports it.
func readAWSProfile(profile string) (key, secret, token string, err error) {
	if !awsProfilePattern.MatchString(profile) {
		return "", "", "", fmt.Errorf("invalid AWS profile name %q: use the name as it appears in "+
			"~/.aws/config (letters, digits, spaces and _ - @ + . %% :, up to 64 characters)", profile)
	}
	home, _ := os.UserHomeDir()
	for _, path := range []string{
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".aws", "config"),
	} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		values := parseAWSIni(string(data), profile)
		if values["access_key_id"] != "" && values["aws_secret_access_key"] != "" {
			return values["access_key_id"], values["aws_secret_access_key"], values["aws_session_token"], nil
		}
		if values["sso_session"] != "" || values["sso_start_url"] != "" {
			return "", "", "", fmt.Errorf("AWS profile %q uses IAM Identity Center (SSO), which this "+
				"build does not resolve. Run `aws configure export-credentials --profile %s` and paste "+
				"the result as static credentials", profile, profile)
		}
	}
	return "", "", "", fmt.Errorf("AWS profile %q was not found in ~/.aws/credentials or ~/.aws/config, "+
		"or it has no access_key_id/aws_secret_access_key", profile)
}

// parseAWSIni returns the key/value pairs of one profile section.
func parseAWSIni(body, profile string) map[string]string {
	out := map[string]string{}
	wanted := "[" + profile + "]"
	current := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name := strings.TrimSpace(strings.Trim(line, "[]"))
			current = name
			if strings.HasPrefix(name, "profile ") {
				current = strings.TrimSpace(strings.TrimPrefix(name, "profile "))
			}
			continue
		}
		if "["+current+"]" != wanted {
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			out[strings.ToLower(strings.TrimSpace(line[:i]))] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}
