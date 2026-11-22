package gateway

// Kiro upstream answers in AWS EventStream binary frames. streamKiro decodes
// them, converts each into an OpenAI chat chunk, and forwards either as OpenAI
// SSE or through the client-format hop (Claude / Responses).

import (
	"bufio"
	"io"
	"net/http"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

func streamKiro(upstream io.Reader, clientFormat, model string, restore map[string]string, w io.Writer, flusher http.Flusher) translate.Usage {
	conv := translate.NewKiroToOpenAI(model, restore)
	var o2c *translate.OpenAIToClaude
	var o2r *translate.OpenAIToResponses
	switch clientFormat {
	case translate.FormatClaude:
		o2c = &translate.OpenAIToClaude{}
	case translate.FormatResponses:
		o2r = &translate.OpenAIToResponses{}
	}
	br := bufio.NewReaderSize(upstream, 64<<10)
	for {
		frame, err := translate.ReadKiroFrame(br)
		if err != nil {
			break
		}
		for _, ch := range conv.Transform(frame) {
			emitChat(w, ch, o2c, o2r)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	finishClientHop(w, o2c, o2r)
	writeDone(w)
	if flusher != nil {
		flusher.Flush()
	}
	return conv.Usage
}
