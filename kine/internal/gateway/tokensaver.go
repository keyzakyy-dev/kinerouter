package gateway

// Token savers applied to the outbound body, mirroring open-sse/handlers/chatCore.js:
// RTK compression of tool results, then caveman / ponytail / custom-prompt system
// injections, in that order. All of it runs on the *translated* provider body so a
// translated and a passthrough request behave the same.
//
// The x-kinerouter-token-saver: off header disables RTK and the two built-in styles,
// not the user's own custom prompt — matching the JS.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/rtk"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

const tokenSaverHeader = "x-kinerouter-token-saver"

type saverCfg struct {
	compress      bool
	headroom      rtk.HeadroomOptions
	headOn        bool
	caveman       string // prompt text, "" when off
	cavemanLevel  string
	ponytail      string
	ponytailLevel string
	custom        string
	// pxpipe runs last and only on Claude-shaped bodies (JS parity).
	pxpipe func(map[string]any) (map[string]any, PxpipeSummary)
}

func (c *saverCfg) active() bool {
	return c.compress || c.headOn || c.caveman != "" || c.ponytail != "" || c.custom != "" || c.pxpipe != nil
}

// saverCfgFrom derives the per-request saver config from settings + headers.
// model/format feed Headroom, which projects the already-translated body.
func saverCfgFrom(g *Gateway, settings map[string]any, h http.Header, model, format string) saverCfg {
	on := !strings.EqualFold(strings.TrimSpace(h.Get(tokenSaverHeader)), "off")
	var c saverCfg
	c.compress = on && boolSetting(settings, "rtkEnabled")
	c.headroom = headroomForChat(settings, model, format)
	c.headOn = on && c.headroom.Enabled
	if g != nil {
		c.pxpipe = g.pxpipeTransformForChat(settings)
	}
	if on && boolSetting(settings, "cavemanEnabled") {
		c.cavemanLevel = strSetting(settings, "cavemanLevel", "full")
		c.caveman = rtk.CavemanPrompts[c.cavemanLevel]
	}
	if on && boolSetting(settings, "ponytailEnabled") {
		c.ponytailLevel = strSetting(settings, "ponytailLevel", "full")
		c.ponytail = rtk.PonytailPrompts[c.ponytailLevel]
	}
	if boolSetting(settings, "customPromptEnabled") {
		c.custom = strings.TrimSpace(strSetting(settings, "customPrompt", ""))
	}
	return c
}

func boolSetting(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func strSetting(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

// apply rewrites body in place and returns the savers that changed something,
// in the shape the console log prints ("RTK:9 · CAVEMAN:full").
func (c *saverCfg) apply(body map[string]any, providerFormat string) []string {
	var parts []string
	if !c.active() || body == nil {
		return parts
	}
	if c.compress {
		if st := rtk.CompressMessages(body, true); st != nil && len(st.Hits) > 0 {
			parts = append(parts, fmt.Sprintf("RTK:%d", len(st.Hits)))
		}
	}
	if c.headOn {
		if st, _ := rtk.CompressWithHeadroom(body, c.headroom); st != nil && st.TokensSaved > 0 {
			parts = append(parts, fmt.Sprintf("HEADROOM:%d", st.TokensSaved))
		}
	}
	if c.caveman != "" {
		rtk.InjectSystemPrompt(body, providerFormat, c.caveman)
		parts = append(parts, "CAVEMAN:"+c.cavemanLevel)
	}
	if c.ponytail != "" {
		rtk.InjectSystemPrompt(body, providerFormat, c.ponytail)
		parts = append(parts, "PONYTAIL:"+c.ponytailLevel)
	}
	if c.custom != "" {
		rtk.InjectSystemPrompt(body, providerFormat, c.custom)
		parts = append(parts, fmt.Sprintf("CUSTOMPROMPT:%dc", len(c.custom)))
	}
	if c.pxpipe != nil && providerFormat == translate.FormatClaude {
		if next, _ := c.pxpipe(body); next != nil {
			replaceMap(body, next)
			parts = append(parts, "PXPIPE")
		}
	}
	return parts
}

// replaceMap makes dst hold src's keys, so the in-place consumers keep working.
func replaceMap(dst, src map[string]any) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}
