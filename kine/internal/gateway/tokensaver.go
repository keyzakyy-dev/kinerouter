package gateway

// Token savers applied to the outbound body, mirroring open-sse/handlers/chatCore.js:
// RTK compression of tool results, then caveman / ponytail / custom-prompt system
// injections, in that order. All of it runs on the *translated* provider body so a
// translated and a passthrough request behave the same.
//
// The x-kinerouter-token-saver: off header disables RTK and the two built-in styles,
// not the user's own custom prompt — matching the JS.

import (
	"net/http"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/rtk"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

const tokenSaverHeader = "x-kinerouter-token-saver"

type saverCfg struct {
	compress bool
	headroom rtk.HeadroomOptions
	headOn   bool
	caveman  string // prompt text, "" when off
	ponytail string
	custom   string
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
		c.caveman = rtk.CavemanPrompts[strSetting(settings, "cavemanLevel", "full")]
	}
	if on && boolSetting(settings, "ponytailEnabled") {
		c.ponytail = rtk.PonytailPrompts[strSetting(settings, "ponytailLevel", "full")]
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

// apply rewrites body in place.
func (c *saverCfg) apply(body map[string]any, providerFormat string) {
	if !c.active() || body == nil {
		return
	}
	if c.compress {
		rtk.CompressMessages(body, true)
	}
	if c.headOn {
		rtk.CompressWithHeadroom(body, c.headroom)
	}
	if c.caveman != "" {
		rtk.InjectSystemPrompt(body, providerFormat, c.caveman)
	}
	if c.ponytail != "" {
		rtk.InjectSystemPrompt(body, providerFormat, c.ponytail)
	}
	if c.custom != "" {
		rtk.InjectSystemPrompt(body, providerFormat, c.custom)
	}
	if c.pxpipe != nil && providerFormat == translate.FormatClaude {
		if next, _ := c.pxpipe(body); next != nil {
			replaceMap(body, next)
		}
	}
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
