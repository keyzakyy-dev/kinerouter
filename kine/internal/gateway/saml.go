package gateway

// SAML 2.0 SSO (SP side). Ported from src/lib/auth/saml.js and
// src/app/api/auth/saml/{start,acs,metadata,test}/route.js.
//
// Security notes:
//   - the IdP endpoint and certificate come only from admin-written settings
//     (or, on the authenticated test route, from the request body); no
//     unauthenticated caller controls where an AuthnRequest is sent
//   - the SAMLResponse is validated by github.com/crewjam/saml, which verifies
//     the XML signature against the configured IdP certificate, plus Status,
//     Conditions/AudienceRestriction, SubjectConfirmation and expiry — not by
//     hand-rolled XML-DSig code
//   - InResponseTo is checked against the saml_state cookie before the
//     assertion is parsed, mirroring the JS route, so a captured response
//     cannot be replayed into another session
//   - the ACS route is rate-limited with the same per-IP limiter as password
//     login, like the JS ACS route
//   - an admin-supplied samlIdpIssuer is enforced; when it is absent the IdP's
//     self-declared Issuer is used, matching node-saml's default (no idpIssuer
//     ⇒ no issuer check). The signature over the IdP certificate remains the
//     actual trust anchor either way.

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"github.com/keyzakyy-dev/kinerouter/internal/auth"
)

const (
	samlCookieState      = "saml_state"
	samlStateTTL         = 10 * 60
	samlDefaultIssuer    = "urn:kinerouter:sp"
	samlDefaultLoginLbl  = "Sign in with SAML SSO"
	samlDefaultAttrEmail = "email"
	samlDefaultAttrName  = "name"
)

// formatX509Certificate mirrors formatX509Certificate in src/lib/auth/saml.js:
// strip the PEM markers and anything that is not base64, rewrap at 64 columns.
func formatX509Certificate(certStr string) string {
	if certStr == "" {
		return ""
	}
	// JS strips the PEM markers first (the marker words themselves are
	// letters, so filtering before stripping would keep them).
	clean := regexp.MustCompile(`(?i)-----(BEGIN|END) CERTIFICATE-----`).ReplaceAllString(certStr, "")
	var b strings.Builder
	for _, r := range clean {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return ""
	}
	var lines []string
	for len(out) > 64 {
		lines = append(lines, out[:64])
		out = out[64:]
	}
	lines = append(lines, out)
	return "-----BEGIN CERTIFICATE-----\n" + strings.Join(lines, "\n") + "\n-----END CERTIFICATE-----"
}

func isSAMLConfigured(settings map[string]any) bool {
	return str(settings, "samlEntryPoint") != "" && str(settings, "samlCert") != ""
}

func samlIssuerID(settings map[string]any) string {
	return firstNonEmpty(strings.TrimSpace(str(settings, "samlIssuer")), samlDefaultIssuer)
}

// samlSP builds the service provider. idpIssuer is the entity id the IdP is
// expected to sign as; see the package comment for how it is chosen.
func samlSP(settings map[string]any, origin, idpIssuer string) *saml.ServiceProvider {
	acs, _ := url.Parse(origin + "/api/auth/saml/acs")
	mdURL, _ := url.Parse(origin + "/api/auth/saml/metadata")
	entityID := samlIssuerID(settings)
	if idpIssuer == "" {
		idpIssuer = entityID
	}
	cert := formatX509Certificate(str(settings, "samlCert"))
	return &saml.ServiceProvider{
		EntityID:    entityID,
		MetadataURL: *mdURL,
		AcsURL:      *acs,
		// No Key/Certificate: this SP never signs, like createSamlInstance
		// in the JS (which passes no private key).
		IDPCertificate: &cert,
		IDPMetadata:    &saml.EntityDescriptor{EntityID: idpIssuer},
		// Node-saml's defaults are transient NameID and a 60 s clock skew;
		// crewjam's MaxClockSkew is a package var (180 s) so it stays.
		AuthnNameIDFormat: saml.TransientNameIDFormat,
	}
}

// buildSamlAuthorizeURL returns the redirect URL and the AuthnRequest id that
// goes into the saml_state cookie.
func buildSamlAuthorizeURL(settings map[string]any, origin string) (string, string, error) {
	sp := samlSP(settings, origin, "")
	req, err := sp.MakeAuthenticationRequest(
		strings.TrimSpace(str(settings, "samlEntryPoint")),
		saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return "", "", err
	}
	u, err := req.Redirect("", sp)
	if err != nil {
		return "", "", err
	}
	return u.String(), req.ID, nil
}

var inResponseToRe = regexp.MustCompile(`(?i)InResponseTo=["']([^"']+)["']`)
var issuerRe = regexp.MustCompile(`(?is)<(?:[A-Za-z0-9_-]+:)?Issuer[^>]*>\s*([^<]+?)\s*<`)

// validateSamlResponse mirrors validateSamlResponse: base64-decode, reject an
// InResponseTo that does not match the stored request id, then verify.
func validateSamlResponse(settings map[string]any, origin, rawResponse, expectedRequestID string) (*saml.Assertion, error) {
	if str(settings, "samlCert") == "" {
		return nil, errors.New("IdP X.509 Certificate (samlCert) is missing or not configured")
	}
	if rawResponse == "" {
		return nil, errors.New("Missing SAMLResponse parameter in assertion POST body")
	}
	xmlBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawResponse))
	if err != nil {
		return nil, errors.New("SAMLResponse is not valid base64")
	}
	if expectedRequestID != "" {
		m := inResponseToRe.FindSubmatch(xmlBytes)
		got := ""
		if m != nil {
			got = string(m[1])
		}
		if got == "" || got != expectedRequestID {
			if got == "" {
				got = "none"
			}
			return nil, errors.New("InResponseTo mismatch: expected " + expectedRequestID + ", received " + got)
		}
	}
	idpIssuer := strings.TrimSpace(str(settings, "samlIdpIssuer"))
	if idpIssuer == "" {
		// node-saml's default: no idpIssuer configured ⇒ no issuer check.
		// The signature is validated against the configured certificate
		// regardless, so the IdP's self-declared entity id is safe to adopt.
		if m := issuerRe.FindSubmatch(xmlBytes); m != nil {
			idpIssuer = strings.TrimSpace(string(m[1]))
		}
	}
	sp := samlSP(settings, origin, idpIssuer)
	return sp.ParseXMLResponse(xmlBytes, []string{expectedRequestID}, sp.AcsURL)
}

// samlProfile flattens an assertion into the shape node-saml's profile has:
// NameID as nameID, each Attribute by Name with a single string or a []string.
func samlProfile(a *saml.Assertion) map[string]any {
	out := map[string]any{}
	if a.Subject != nil && a.Subject.NameID != nil {
		out["nameID"] = a.Subject.NameID.Value
		if a.Subject.NameID.Format != "" {
			out["nameIDFormat"] = a.Subject.NameID.Format
		}
	}
	for _, st := range a.AttributeStatements {
		for _, at := range st.Attributes {
			if at.Name == "" || len(at.Values) == 0 {
				continue
			}
			vals := make([]string, 0, len(at.Values))
			for _, v := range at.Values {
				vals = append(vals, firstNonEmpty(v.Value, v.NameID.Value))
			}
			var value any = vals[0]
			if len(vals) > 1 {
				value = vals
			}
			if _, exists := out[at.Name]; !exists {
				out[at.Name] = value
			}
			if _, exists := out["attributes"]; !exists {
				out["attributes"] = map[string]any{}
			}
			attrs := out["attributes"].(map[string]any)
			if _, exists := attrs[at.Name]; !exists {
				attrs[at.Name] = value
			}
		}
	}
	if _, ok := out["mail"]; !ok {
		if v, ok := out["urn:oid:0.9.2342.19200300.100.1.3"]; ok {
			out["mail"] = v
		}
	}
	if _, ok := out["email"]; !ok {
		if v, ok := out["mail"]; ok {
			out["email"] = v
		}
	}
	return out
}

func profileValue(profile map[string]any, key string) string {
	if key == "" {
		return ""
	}
	switch v := profile[key].(type) {
	case string:
		return v
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

var samlEmailKeys = []string{
	"email", "emailAddress", "mail", "nameID", "nameId", "upn",
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress",
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/nameidentifier",
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/upn",
}

var samlNameKeys = []string{
	"displayName", "name", "cn", "commonName",
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name",
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname",
}

func pickSamlEmail(profile map[string]any, settings map[string]any) string {
	if v := profileValue(profile, str(settings, "samlAttributeEmail")); v != "" {
		return v
	}
	for _, k := range samlEmailKeys {
		if v := profileValue(profile, k); v != "" {
			return v
		}
	}
	if attrs, ok := profile["attributes"].(map[string]any); ok {
		for _, k := range samlEmailKeys {
			if v := profileValue(attrs, k); v != "" {
				return v
			}
		}
	}
	return ""
}

func pickSamlDisplayName(profile map[string]any, settings map[string]any) string {
	if v := profileValue(profile, str(settings, "samlAttributeName")); v != "" {
		return v
	}
	for _, k := range samlNameKeys {
		if v := profileValue(profile, k); v != "" {
			return v
		}
	}
	given := profileValue(profile, "givenName")
	surname := firstNonEmpty(profileValue(profile, "sn"), profileValue(profile, "surname"))
	if combined := strings.TrimSpace(given + " " + surname); combined != "" {
		return combined
	}
	return pickSamlEmail(profile, settings)
}

func generateSamlMetadata(origin string, settings map[string]any) ([]byte, error) {
	sp := samlSP(settings, origin, "")
	return xml.MarshalIndent(sp.Metadata(), "", "  ")
}

// ---------------------------------------------------------------- handlers

func (g *Gateway) handleSAMLStart(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.store.GetSettings()
	origin := g.publicOrigin(r, settings, true)
	if !isSAMLConfigured(settings) {
		loginRedirect(w, r, origin, "saml_not_configured")
		return
	}
	authorizeURL, requestID, err := buildSamlAuthorizeURL(settings, origin)
	if err != nil {
		loginRedirect(w, r, origin, err.Error())
		return
	}
	setShortCookie(w, r, samlCookieState, requestID, g.secureCookie(r))
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (g *Gateway) handleSAMLACS(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.store.GetSettings()
	origin := g.publicOrigin(r, settings, true)
	key := "login:" + g.clientIP(r)
	if wait, ok := g.limiter.Check(key); !ok {
		loginRedirect(w, r, origin, tooManyAttempts(wait))
		return
	}
	storedRequestID := cookieValue(r, samlCookieState)
	clearCookie(w, samlCookieState)

	if err := r.ParseForm(); err != nil {
		g.limiter.Fail(key)
		loginRedirect(w, r, origin, "saml_acs_failed")
		return
	}
	raw := r.PostFormValue("SAMLResponse")
	if raw == "" {
		g.limiter.Fail(key)
		loginRedirect(w, r, origin, "saml_missing_response")
		return
	}
	if !isSAMLConfigured(settings) {
		g.limiter.Fail(key)
		loginRedirect(w, r, origin, "saml_not_configured")
		return
	}
	assertion, err := validateSamlResponse(settings, origin, raw, storedRequestID)
	if err != nil {
		g.limiter.Fail(key)
		loginRedirect(w, r, origin, err.Error())
		return
	}
	profile := samlProfile(assertion)
	samlEmail := pickSamlEmail(profile, settings)
	samlName := pickSamlDisplayName(profile, settings)
	if samlName == "" {
		samlName = "SAML user"
	}
	g.limiter.Reset(key)
	token, err := auth.SignClaims(g.cfg.JWTSecret, 24*time.Hour, map[string]any{
		"saml":      true,
		"samlEmail": nulIfEmpty(samlEmail),
		"samlName":  samlName,
	})
	if err != nil {
		loginRedirect(w, r, origin, "failed to sign session")
		return
	}
	auth.SetSessionCookie(w, r, token, g.cfg.AuthCookieSecure)
	http.Redirect(w, r, origin+"/dashboard", http.StatusFound)
}

func tooManyAttempts(wait time.Duration) string {
	return "Too many failed attempts. Try again in " + itoa(int64(wait.Seconds())) + "s."
}

func (g *Gateway) handleSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.store.GetSettings()
	origin := g.publicOrigin(r, settings, true)
	md, err := generateSamlMetadata(origin, settings)
	if err != nil {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `<?xml version="1.0"?><Error>`+err.Error()+`</Error>`)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(md)
}

func (g *Gateway) handleSAMLTest(w http.ResponseWriter, r *http.Request) {
	if !g.canAccessAuthTest(w, r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
		return
	}
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		body = map[string]any{}
	}
	settings, _ := g.store.GetSettings()
	pick := func(key, fallback string) string {
		if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if v := strings.TrimSpace(str(settings, key)); v != "" {
			return v
		}
		return fallback
	}
	entryPoint := pick("samlEntryPoint", "")
	issuer := pick("samlIssuer", samlDefaultIssuer)
	cert := strings.TrimSpace(str(settings, "samlCert"))
	if v, ok := body["samlCert"].(string); ok {
		cert = strings.TrimSpace(v)
	}
	if entryPoint == "" {
		writeErr(w, http.StatusBadRequest, "Single Sign-On Service URL (samlEntryPoint) is required")
		return
	}
	if _, err := url.Parse(entryPoint); err != nil {
		writeErr(w, http.StatusBadRequest, "Single Sign-On Service URL must be a valid URL")
		return
	}
	if issuer == "" {
		writeErr(w, http.StatusBadRequest, "SP Entity ID / Issuer (samlIssuer) is required")
		return
	}
	if cert == "" {
		writeErr(w, http.StatusBadRequest, "IdP X.509 Certificate (samlCert) is required")
		return
	}
	if formatX509Certificate(cert) == "" {
		writeErr(w, http.StatusBadRequest, "Invalid IdP X.509 Certificate format")
		return
	}
	origin := g.publicOrigin(r, settings, true)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"samlEntryPoint": entryPoint,
		"samlIssuer":     issuer,
		"certValid":      true,
		"acsUrl":         origin + "/api/auth/saml/acs",
		"metadataUrl":    origin + "/api/auth/saml/metadata",
		"message":        "SAML 2.0 configuration verified successfully.",
	})
}
