package xmlrpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEncodeRequestNestedArray(t *testing.T) {
	// The append call passes post-processing parameters as an array of pairs,
	// which is the only nesting NZBGet needs from the encoder.
	body, err := encodeRequest("append", []any{"", "url", 0, false, []any{[]any{"*unpack:", "yes"}}})
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"<methodName>append</methodName>",
		"<boolean>0</boolean>",
		"<i4>0</i4>",
		"<array><data><value><array><data><value><string>*unpack:</string></value>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("encoded request missing %q\ngot: %s", want, got)
		}
	}
}

func TestCallDecodesStructArray(t *testing.T) {
	const response = `<?xml version="1.0"?>
<methodResponse><params><param><value><array><data>
  <value><struct>
    <member><name>NZBID</name><value><i4>42</i4></value></member>
    <member><name>NZBName</name><value><string>Some &amp; Thing</string></value></member>
    <member><name>FileSizeLo</name><value><i4>-2147483648</i4></value></member>
    <member><name>Paused</name><value><boolean>1</boolean></value></member>
    <member><name>Bare</name><value>plain text</value></member>
  </struct></value>
</data></array></value></param></params></methodResponse>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, response)
	}))
	defer server.Close()

	result, err := New(server.URL).Call(context.Background(), "listgroups")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("result = %#v, want one-element array", result)
	}
	members := items[0].(map[string]any)

	if members["NZBID"] != int64(42) {
		t.Errorf("NZBID = %#v, want 42", members["NZBID"])
	}
	if members["NZBName"] != "Some & Thing" {
		t.Errorf("NZBName = %#v", members["NZBName"])
	}
	if members["FileSizeLo"] != int64(-2147483648) {
		t.Errorf("FileSizeLo = %#v", members["FileSizeLo"])
	}
	if members["Paused"] != true {
		t.Errorf("Paused = %#v", members["Paused"])
	}
	if members["Bare"] != "plain text" {
		t.Errorf("Bare = %#v, want an untyped value read as a string", members["Bare"])
	}
}

func TestCallReturnsFault(t *testing.T) {
	const response = `<?xml version="1.0"?>
<methodResponse><fault><value><struct>
  <member><name>faultCode</name><value><i4>2</i4></value></member>
  <member><name>faultString</name><value><string>Invalid method</string></value></member>
</struct></value></fault></methodResponse>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, response)
	}))
	defer server.Close()

	_, err := New(server.URL).Call(context.Background(), "nope")
	fault, ok := err.(*Fault)
	if !ok {
		t.Fatalf("err = %v (%T), want *Fault", err, err)
	}
	if fault.Code != 2 || fault.Message != "Invalid method" {
		t.Errorf("fault = %+v", fault)
	}
}
