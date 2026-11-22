package executor

// Bedrock transport. Ported from open-sse/executors/bedrock.js: SigV4-signed
// POST to /model/<escaped id>/<invoke|invoke-with-response-stream>; the Anthropic
// wire drops `model` and `stream` and pins anthropic_version; streaming arrives
// as AWS EventStream frames wrapping base64 SSE payloads and is unwrapped here
// rather than in a translator, for the same reason kiro decodes its own framing.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

const bedrockAnthropicVersion = "bedrock-2023-05-31"

// bedrockWire says which body/response shape a provider entry speaks. Both
// Bedrock ids share this transport; the registry format decides the shape.
func bedrockWire(p string) (wire string, ok bool) {
	switch p {
	case "bedrock":
		return "claude", true
	case "bedrock-xai":
		return "openai", true
	}
	return "", false
}

var bedrockFamily = map[string]*regexp.Regexp{
	"claude": regexp.MustCompile(`(^|[./])anthropic\.`),
	"openai": regexp.MustCompile(`(^|[./])xai\.`),
}

// bedrockURL builds the runtime endpoint. The model id is escaped once here;
// signAWSRequest escapes it again for the canonical request, which is what a
// ":0"-suffixed version id needs.
func bedrockURL(model, region string, stream bool) string {
	action := "invoke"
	if stream {
		action = "invoke-with-response-stream"
	}
	return "https://bedrock-runtime." + region + ".amazonaws.com/model/" +
		escapeAWSUri(model) + "/" + action
}

// bedrockBody drops the two fields Bedrock takes from the URL/endpoint instead
// of the body, and pins the Anthropic version after the spread so a claude
// client's own value cannot win (Bedrock answers ValidationException for that).
func bedrockBody(raw []byte, wire string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	delete(body, "model")
	delete(body, "stream")
	if wire == "claude" {
		body["anthropic_version"] = bedrockAnthropicVersion
	}
	return json.Marshal(body)
}

func (e *Executor) executeBedrock(ctx context.Context, req Request) (*Response, error) {
	wire, ok := bedrockWire(req.Provider)
	if !ok {
		return nil, fmt.Errorf("not a bedrock provider: %q", req.Provider)
	}
	if re := bedrockFamily[wire]; re != nil && !re.MatchString(req.Model) {
		hint := "an Anthropic (anthropic.*) model id"
		if wire == "openai" {
			hint = "an xAI (xai.*) model id"
		}
		return nil, fmt.Errorf("bedrock model %q is not supported by provider %q, which expects %s; "+
			"model families on Bedrock use different request and response shapes", req.Model, req.Provider, hint)
	}
	creds, err := resolveAWSCredentials(req.Creds)
	if err != nil {
		return nil, err
	}
	body, err := bedrockBody(req.Body, wire)
	if err != nil {
		return nil, err
	}
	endpoint := req.URLOverride
	if endpoint == "" {
		endpoint = bedrockURL(req.Model, creds.Region, req.Stream)
	}
	accept := "application/json"
	if req.Stream {
		accept = "application/vnd.amazon.eventstream"
	}
	headers, err := signAWSRequest("POST", endpoint, map[string]string{
		"Content-Type": "application/json",
		"Accept":       accept,
	}, string(body), creds.Region, "bedrock", creds, time.Now())
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	client := e.client
	if req.Creds.ProxyURL != "" {
		if u, err := url.Parse(req.Creds.ProxyURL); err == nil {
			client = e.clientWithProxy(u)
		}
	}
	// Bedrock never redirects. Following one would replay the body and the
	// signed x-amz-security-token to whatever origin the redirect names.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !req.Stream {
		return &Response{Status: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
	}
	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		err := eventStreamToSSE(resp.Body, pw, wire == "claude")
		pw.CloseWithError(err)
	}()
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: pr}, nil
}

// eventStreamToSSE unwraps EventStream frames into the SSE the rest of the
// pipeline already understands: named Claude events, or bare OpenAI `data:`
// chunks terminated by [DONE].
func eventStreamToSSE(upstream io.Reader, w io.Writer, claudeWire bool) error {
	br := bufio.NewReaderSize(upstream, 64<<10)
	sawTerminal := false
	for {
		frame, err := translate.ReadKiroFrame(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			// A truncated frame mid-stream is an integrity failure, not a clean
			// end; surface it instead of letting the client see a short answer.
			fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"api_error\"}}\n\n",
				"AWS EventStream frame failed: "+err.Error())
			return err
		}
		msgType, _ := frame.Headers[":message-type"].(string)
		if msgType == "error" || msgType == "exception" {
			msg, _ := frame.Payload["message"].(string)
			if msg == "" {
				msg = "Bedrock upstream sent an EventStream " + msgType
			}
			fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"api_error\"}}\n\n", msg)
			return fmt.Errorf("bedrock %s: %s", msgType, msg)
		}
		eventType, _ := frame.Headers[":event-type"].(string)
		if eventType != "chunk" {
			continue
		}
		encoded, _ := frame.Payload["bytes"].(string)
		if encoded == "" {
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		var ev map[string]any
		if json.Unmarshal(payload, &ev) != nil {
			continue
		}
		if claudeWire {
			event, _ := ev["type"].(string)
			if event == "message_stop" {
				sawTerminal = true
			}
			if event != "" {
				fmt.Fprintf(w, "event: %s\n", event)
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			continue
		}
		if _, hasChoices := ev["choices"]; !hasChoices {
			continue
		}
		if list, ok := ev["choices"].([]any); ok {
			for _, ch := range list {
				if c, ok := ch.(map[string]any); ok && c["finish_reason"] != nil {
					sawTerminal = true
				}
			}
		}
		fmt.Fprintf(w, "data: %s\n\n", payload)
	}
	// ponytail: message_stop is not required — a client that disconnects mid
	// answer never reaches it, and the frames already delivered are the answer.
	_ = sawTerminal
	if !claudeWire {
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
	return nil
}
