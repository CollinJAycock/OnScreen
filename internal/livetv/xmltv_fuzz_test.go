package livetv

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzParseXMLTV exercises ParseXMLTV against adversarial XML. Same
// rationale as the M3U fuzz: admin URLs feed straight into this and a
// crash here would kill EPG refresh for everyone on the server.
//
// XML parsers historically have a long tail of problems (huge
// attribute values, deeply nested entities, malformed timestamps,
// unicode in element names). The standard library xml.Decoder handles
// most but the date-parsing layer on top can panic on bad inputs.
//
// Run as: go test -fuzz=FuzzParseXMLTV -fuzztime=30s ./internal/livetv/
func FuzzParseXMLTV(f *testing.F) {
	// Seeds: a representative valid sample plus shapes known to trip
	// XML parsers historically.
	f.Add([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<tv generator-info-name="test">
  <channel id="C1.test"><display-name>Channel One</display-name><lcn>1</lcn></channel>
  <programme start="20260426120000 +0000" stop="20260426130000 +0000" channel="C1.test">
    <title lang="en">Show</title>
    <desc>desc</desc>
    <episode-num system="onscreen">1.5</episode-num>
  </programme>
</tv>`))
	f.Add([]byte(``))                                                    // empty
	f.Add([]byte(`<tv></tv>`))                                          // empty document
	f.Add([]byte(`<tv><programme start="bogus" channel="x"/></tv>`))    // malformed timestamp
	f.Add([]byte(`<tv><channel id=""><display-name></display-name></channel></tv>`)) // empty fields
	f.Add([]byte(`<tv><programme start="20260426120000" channel="x"/></tv>`)) // no TZ offset
	f.Add([]byte(strings.Repeat(`<tv>`, 1000)))                         // deep nesting
	f.Add([]byte(`<tv><programme start="20260426120000 +9999" channel="x"/></tv>`)) // bogus offset

	f.Fuzz(func(t *testing.T, data []byte) {
		// Don't care about the parsed shape — only that the parser
		// returns without panic. Skipped count + err are the documented
		// degradation paths; either is fine.
		_, _, _, _ = ParseXMLTV(bytes.NewReader(data))
	})
}
