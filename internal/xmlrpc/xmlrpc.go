// Package xmlrpc is a minimal XML-RPC client, enough for NZBGet's API.
//
// The Go ecosystem has no XML-RPC client in the standard library and the
// third-party ones decode into structs by reflection, which fits NZBGet badly:
// its responses are large heterogeneous structs whose fields differ per
// download state. Decoding into map[string]any and reading fields by name --
// the same shape the Python bot worked with -- keeps the call sites simple.
package xmlrpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client calls a single XML-RPC endpoint.
type Client struct {
	url  string
	http *http.Client
}

// New returns a client for the given endpoint URL.
func New(url string) *Client {
	return &Client{
		url:  url,
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// Fault is a fault response from the server.
type Fault struct {
	Code    int
	Message string
}

func (f *Fault) Error() string {
	return fmt.Sprintf("xmlrpc fault %d: %s", f.Code, f.Message)
}

// Call invokes method with the given arguments and returns the decoded result.
func (c *Client) Call(ctx context.Context, method string, args ...any) (any, error) {
	body, err := encodeRequest(method, args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("xmlrpc: %s returned %s", method, resp.Status)
	}
	return decodeResponse(resp.Body)
}

func encodeRequest(method string, args []any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	xml.EscapeText(&b, []byte(method))
	b.WriteString(`</methodName><params>`)
	for _, arg := range args {
		b.WriteString("<param>")
		if err := encodeValue(&b, arg); err != nil {
			return nil, err
		}
		b.WriteString("</param>")
	}
	b.WriteString("</params></methodCall>")
	return b.Bytes(), nil
}

func encodeValue(b *bytes.Buffer, v any) error {
	b.WriteString("<value>")
	defer b.WriteString("</value>")

	switch value := v.(type) {
	case string:
		b.WriteString("<string>")
		xml.EscapeText(b, []byte(value))
		b.WriteString("</string>")
	case bool:
		b.WriteString("<boolean>")
		if value {
			b.WriteString("1")
		} else {
			b.WriteString("0")
		}
		b.WriteString("</boolean>")
	case int:
		fmt.Fprintf(b, "<i4>%d</i4>", value)
	case int64:
		fmt.Fprintf(b, "<i4>%d</i4>", value)
	case float64:
		fmt.Fprintf(b, "<double>%s</double>", strconv.FormatFloat(value, 'f', -1, 64))
	case []byte:
		fmt.Fprintf(b, "<base64>%s</base64>", base64.StdEncoding.EncodeToString(value))
	case []any:
		b.WriteString("<array><data>")
		for _, item := range value {
			if err := encodeValue(b, item); err != nil {
				return err
			}
		}
		b.WriteString("</data></array>")
	case []int:
		b.WriteString("<array><data>")
		for _, item := range value {
			if err := encodeValue(b, item); err != nil {
				return err
			}
		}
		b.WriteString("</data></array>")
	case []int64:
		b.WriteString("<array><data>")
		for _, item := range value {
			if err := encodeValue(b, item); err != nil {
				return err
			}
		}
		b.WriteString("</data></array>")
	case []string:
		b.WriteString("<array><data>")
		for _, item := range value {
			if err := encodeValue(b, item); err != nil {
				return err
			}
		}
		b.WriteString("</data></array>")
	case map[string]any:
		b.WriteString("<struct>")
		for name, member := range value {
			b.WriteString("<member><name>")
			xml.EscapeText(b, []byte(name))
			b.WriteString("</name>")
			if err := encodeValue(b, member); err != nil {
				return err
			}
			b.WriteString("</member>")
		}
		b.WriteString("</struct>")
	default:
		return fmt.Errorf("xmlrpc: cannot encode %T", v)
	}
	return nil
}

func decodeResponse(r io.Reader) (any, error) {
	dec := xml.NewDecoder(r)
	isFault := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("xmlrpc: response has no value")
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "fault":
			isFault = true
		case "value":
			value, err := decodeValue(dec, start)
			if err != nil {
				return nil, err
			}
			if isFault {
				return nil, asFault(value)
			}
			return value, nil
		}
	}
}

func asFault(value any) error {
	fault := &Fault{Message: fmt.Sprint(value)}
	if members, ok := value.(map[string]any); ok {
		if code, ok := members["faultCode"].(int64); ok {
			fault.Code = int(code)
		}
		if message, ok := members["faultString"].(string); ok {
			fault.Message = message
		}
	}
	return fault
}

// decodeValue reads one <value> element, positioned just after its start tag.
func decodeValue(dec *xml.Decoder, start xml.StartElement) (any, error) {
	var (
		result   any
		text     strings.Builder
		hasTyped bool
	)

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.CharData:
			if !hasTyped {
				text.Write(t)
			}
		case xml.StartElement:
			hasTyped = true
			result, err = decodeTyped(dec, t)
			if err != nil {
				return nil, err
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if hasTyped {
					return result, nil
				}
				// A <value> with no type child is a string.
				return text.String(), nil
			}
		}
	}
}

func decodeTyped(dec *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "array":
		return decodeArray(dec, start)
	case "struct":
		return decodeStruct(dec, start)
	}

	var raw string
	if err := dec.DecodeElement(&raw, &start); err != nil {
		return nil, err
	}
	switch start.Name.Local {
	case "int", "i4", "i8":
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case "boolean":
		return strings.TrimSpace(raw) == "1", nil
	case "double":
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	case "base64":
		return base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	case "dateTime.iso8601":
		return raw, nil
	default: // string, nil, anything unknown
		return raw, nil
	}
}

func decodeArray(dec *xml.Decoder, start xml.StartElement) (any, error) {
	items := []any{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				item, err := decodeValue(dec, t)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return items, nil
			}
		}
	}
}

func decodeStruct(dec *xml.Decoder, start xml.StartElement) (any, error) {
	members := map[string]any{}
	var name string
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "name":
				if err := dec.DecodeElement(&name, &t); err != nil {
					return nil, err
				}
				name = strings.TrimSpace(name)
			case "value":
				value, err := decodeValue(dec, t)
				if err != nil {
					return nil, err
				}
				members[name] = value
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return members, nil
			}
		}
	}
}
