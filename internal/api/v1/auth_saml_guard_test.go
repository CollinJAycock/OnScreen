package v1

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	"github.com/crewjam/saml/xmlenc"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

const (
	guardResponseOpen = `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="r1" Version="2.0">` +
		`<saml:Issuer>https://idp.example</saml:Issuer>`
	guardResponseClose = `</samlp:Response>`
)

// guardAssertion builds a saml:Assertion whose Subject holds subjectInner.
func guardAssertion(subjectInner string) string {
	return `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="a1" Version="2.0">` +
		`<saml:Issuer>https://idp.example</saml:Issuer>` +
		`<saml:Subject>` + subjectInner + `</saml:Subject>` +
		`</saml:Assertion>`
}

const goodNameID = `<saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">alice@example.com</saml:NameID>` +
	`<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"/>`

// guardRequest returns a parsed POST /acs request carrying xml as the
// SAMLResponse, plus extra form fields.
func guardRequest(t *testing.T, xml string, query string, extra url.Values) *http.Request {
	t.Helper()
	form := url.Values{}
	if xml != "" {
		form.Set("SAMLResponse", base64.StdEncoding.EncodeToString([]byte(xml)))
	}
	for k, v := range extra {
		form[k] = v
	}
	target := "/api/v1/auth/saml/acs"
	if query != "" {
		target += "?" + query
	}
	r := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	return r
}

func guardKeyPair(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	certPEM, keyPEM, err := GenerateSPKeyPair("onscreen-guard-test")
	if err != nil {
		t.Fatalf("GenerateSPKeyPair: %v", err)
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return leaf, pair.PrivateKey.(*rsa.PrivateKey)
}

// encryptAssertion wraps assertionXML in a saml:EncryptedAssertion the way an
// IdP does (RSA-OAEP key transport, EncryptedKey inside EncryptedData).
func encryptAssertion(t *testing.T, cert *x509.Certificate, assertionXML string) string {
	t.Helper()
	encData, err := xmlenc.OAEP().Encrypt(cert, []byte(assertionXML), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	doc := etree.NewDocument()
	wrap := doc.CreateElement("saml:EncryptedAssertion")
	wrap.CreateAttr("xmlns:saml", samlAssertionNS)
	wrap.AddChild(encData)
	out, err := doc.WriteToString()
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	return out
}

func TestSAMLGuard(t *testing.T) {
	cert, key := guardKeyPair(t)
	_, otherKey := guardKeyPair(t)

	cases := []struct {
		name    string
		xml     string
		query   string
		extra   url.Values
		key     any
		refused bool
	}{
		{name: "plain assertion passes",
			xml: guardResponseOpen + guardAssertion(goodNameID) + guardResponseClose},
		{name: "default-namespace assertion passes",
			xml: guardResponseOpen +
				`<Assertion xmlns="urn:oasis:names:tc:SAML:2.0:assertion" ID="a1" Version="2.0"><Issuer>https://idp.example</Issuer>` +
				`<Subject><NameID>alice@example.com</NameID></Subject></Assertion>` + guardResponseClose},
		{name: "foreign-namespace NameID refused (GO-2026-6909)", refused: true,
			xml: guardResponseOpen + guardAssertion(`<x:NameID xmlns:x="urn:evil">admin@example.com</x:NameID>`) + guardResponseClose},
		{name: "foreign NameID next to a real one refused", refused: true,
			xml: guardResponseOpen + guardAssertion(goodNameID+`<NameID xmlns="urn:evil">admin@example.com</NameID>`) + guardResponseClose},
		{name: "two SAML NameIDs refused", refused: true,
			xml: guardResponseOpen + guardAssertion(goodNameID+`<saml:NameID>admin@example.com</saml:NameID>`) + guardResponseClose},
		{name: "assertion without Subject refused", refused: true,
			xml: guardResponseOpen + `<saml:Assertion ID="a1" Version="2.0"><saml:Issuer>https://idp.example</saml:Issuer></saml:Assertion>` + guardResponseClose},
		{name: "assertion without Issuer refused", refused: true,
			xml: guardResponseOpen + `<saml:Assertion ID="a1" Version="2.0"><saml:Subject>` + goodNameID + `</saml:Subject></saml:Assertion>` + guardResponseClose},
		{name: "artifact binding in the body refused", refused: true,
			extra: url.Values{"SAMLart": {"AAQAA..."}}},
		{name: "artifact binding in the query refused", refused: true,
			query: "SAMLart=AAQAA..."},
		{name: "undecodable response left to the library",
			extra: url.Values{"SAMLResponse": {"%%% not base64"}}},
		{name: "non-XML response left to the library",
			xml: "not xml at all"},
		{name: "encrypted assertion passes", key: key,
			xml: guardResponseOpen + encryptAssertion(t, cert, guardAssertion(goodNameID)) + guardResponseClose},
		{name: "encrypted assertion with foreign NameID refused", key: key, refused: true,
			xml: guardResponseOpen + encryptAssertion(t, cert, guardAssertion(`<x:NameID xmlns:x="urn:evil">admin@example.com</x:NameID>`)) + guardResponseClose},
		{name: "encrypted assertion for another key refused", key: otherKey, refused: true,
			xml: guardResponseOpen + encryptAssertion(t, cert, guardAssertion(goodNameID)) + guardResponseClose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := guardRequest(t, tc.xml, tc.query, tc.extra)
			k := tc.key
			if k == nil {
				k = key
			}
			err := checkSAMLResponseShape(r, k)
			if tc.refused && !errors.Is(err, errSAMLShape) {
				t.Fatalf("want refusal, got %v", err)
			}
			if !tc.refused && err != nil {
				t.Fatalf("want pass, got %v", err)
			}
		})
	}
}

// A signed assertion may omit NameID (or the whole Subject). The profile must
// come back with an empty Subject so ACS answers SAML_NO_SUBJECT, not panic.
func TestBuildSAMLProfile_NoNameIDDoesNotPanic(t *testing.T) {
	for name, a := range map[string]*saml.Assertion{
		"no subject": {Issuer: saml.Issuer{Value: "https://idp.example"}},
		"no nameid":  {Issuer: saml.Issuer{Value: "https://idp.example"}, Subject: &saml.Subject{}},
	} {
		t.Run(name, func(t *testing.T) {
			p := buildSAMLProfile(a, settings.SAMLConfig{})
			if p.Subject != "" {
				t.Fatalf("Subject = %q, want empty", p.Subject)
			}
		})
	}
}

// crewjam/saml before v0.6 filled Subject.NameID from a NameID in any
// namespace (GO-2026-6909), which is why checkSAMLResponseShape exists.
// v0.6.3 decodes it by namespace and leaves NameID nil. This keeps it that
// way: if a downgrade or a regression brings the old decoding back, the
// guard's NameID rule is the only thing left refusing it.
func TestSAMLGuard_UpstreamRejectsForeignNameID(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(guardAssertion(`<x:NameID xmlns:x="urn:evil">admin@example.com</x:NameID>`)); err != nil {
		t.Fatal(err)
	}
	var a saml.Assertion
	if err := unmarshalEtree(doc.Root(), &a); err != nil {
		t.Fatal(err)
	}
	if a.Subject != nil && a.Subject.NameID != nil {
		t.Fatalf("crewjam/saml took a foreign-namespace NameID as the subject (%q); see the comment above", a.Subject.NameID.Value)
	}
}

func unmarshalEtree(el *etree.Element, v any) error {
	d := etree.NewDocument()
	d.SetRoot(el.Copy())
	b, err := d.WriteToBytes()
	if err != nil {
		return err
	}
	return xml.Unmarshal(b, v)
}
