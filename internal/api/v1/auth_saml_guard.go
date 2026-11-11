package v1

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/beevik/etree"
	"github.com/crewjam/saml/xmlenc"
)

// Namespaces the SAML guard checks against.
const (
	samlAssertionNS = "urn:oasis:names:tc:SAML:2.0:assertion"
	xmlEncNS        = "http://www.w3.org/2001/04/xmlenc#"
)

// errSAMLShape marks a SAML response the guard refused before the library
// parsed it. The user sees the same "could not be verified" error as for any
// other bad assertion; the wrapped detail is for the server log.
var errSAMLShape = errors.New("saml response refused by shape check")

// checkSAMLResponseShape inspects the raw SAML response for shapes that
// github.com/crewjam/saml before v0.6 mishandled (GO-2026-6909), and refuses
// them before ParseResponse runs. v0.6.3 fixes both library bugs, but the
// advisory lists no fixed version yet and the check is cheap, so it stays as
// defence in depth: every case below still needs an assertion signed by the
// configured IdP, and the library still verifies the signature afterwards.
//
//   - Before v0.6, Subject.NameID was decoded with a bare `xml:"NameID"` tag,
//     so a NameID in any namespace filled it. OnScreen identifies SAML users by NameID, so a
//     foreign-namespace NameID could sign someone in as another user. Every
//     direct child of a SAML Subject must be in the SAML assertion namespace,
//     and there may be at most one NameID.
//   - An assertion without a Subject makes the library dereference nil while
//     validating it. Every assertion must have a Subject and an Issuer.
//   - The artifact binding (SAMLart) makes the library fetch the response from
//     the IdP itself, where this check can't see it. OnScreen's sign-in uses
//     the POST binding, so artifacts are refused.
//
// Encrypted assertions are decrypted with the SP key the same way the library
// does, then checked. Undecodable input returns nil: the library rejects it
// with its own, already-logged error.
//
// r must already be parsed (ParseForm). Like the library, SAMLart is read from
// r.Form (body or query) and SAMLResponse from r.PostForm.
func checkSAMLResponseShape(r *http.Request, spKey any) error {
	if r.Form.Get("SAMLart") != "" {
		return fmt.Errorf("%w: the artifact binding (SAMLart) is not supported; configure the IdP to use HTTP-POST", errSAMLShape)
	}
	raw, err := base64.StdEncoding.DecodeString(r.PostForm.Get("SAMLResponse"))
	if err != nil || len(raw) == 0 {
		return nil
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(raw); err != nil || doc.Root() == nil {
		return nil
	}
	return checkSAMLElement(doc.Root(), spKey, 0)
}

// checkSAMLElement applies the shape rules to el and everything under it.
// depth counts decryptions, so an encrypted assertion can't nest forever.
func checkSAMLElement(el *etree.Element, spKey any, depth int) error {
	if el.NamespaceURI() == samlAssertionNS {
		switch el.Tag {
		case "Assertion":
			if samlChild(el, "Issuer") == nil {
				return fmt.Errorf("%w: assertion has no Issuer", errSAMLShape)
			}
			if samlChild(el, "Subject") == nil {
				return fmt.Errorf("%w: assertion has no Subject", errSAMLShape)
			}
		case "Subject":
			nameIDs := 0
			for _, c := range el.ChildElements() {
				if ns := c.NamespaceURI(); ns != samlAssertionNS {
					return fmt.Errorf("%w: Subject child <%s> is in namespace %q", errSAMLShape, c.Tag, ns)
				}
				if c.Tag == "NameID" {
					nameIDs++
				}
			}
			if nameIDs > 1 {
				return fmt.Errorf("%w: Subject has %d NameIDs", errSAMLShape, nameIDs)
			}
		case "EncryptedAssertion":
			if depth > 0 {
				return fmt.Errorf("%w: nested EncryptedAssertion", errSAMLShape)
			}
			plain, err := decryptSAMLAssertion(el, spKey)
			if err != nil {
				return fmt.Errorf("%w: EncryptedAssertion: %v", errSAMLShape, err)
			}
			if err := checkSAMLElement(plain, spKey, depth+1); err != nil {
				return err
			}
		}
	}
	for _, c := range el.ChildElements() {
		if err := checkSAMLElement(c, spKey, depth); err != nil {
			return err
		}
	}
	return nil
}

// samlChild returns el's first direct child named tag in the SAML assertion
// namespace, or nil.
func samlChild(el *etree.Element, tag string) *etree.Element {
	for _, c := range el.ChildElements() {
		if c.Tag == tag && c.NamespaceURI() == samlAssertionNS {
			return c
		}
	}
	return nil
}

// decryptSAMLAssertion mirrors crewjam/saml's ServiceProvider.decryptElement:
// the EncryptedKey is either a direct child of the EncryptedAssertion or inside
// EncryptedData's KeyInfo (where xmlenc.Decrypt finds it with the SP key).
func decryptSAMLAssertion(encryptedEl *etree.Element, spKey any) (*etree.Element, error) {
	var dataEl *etree.Element
	for _, c := range encryptedEl.ChildElements() {
		if c.Tag == "EncryptedData" && c.NamespaceURI() == xmlEncNS {
			if dataEl != nil {
				return nil, errors.New("more than one EncryptedData")
			}
			dataEl = c
		}
	}
	if dataEl == nil {
		return nil, errors.New("no EncryptedData")
	}
	key := spKey
	if keyEl := encryptedEl.FindElement("./EncryptedKey"); keyEl != nil {
		k, err := xmlenc.Decrypt(spKey, keyEl)
		if err != nil {
			return nil, fmt.Errorf("decrypt key: %w", err)
		}
		key = k
	}
	plaintext, err := xmlenc.Decrypt(key, dataEl)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(plaintext); err != nil || doc.Root() == nil {
		return nil, errors.New("decrypted assertion is not XML")
	}
	return doc.Root(), nil
}
