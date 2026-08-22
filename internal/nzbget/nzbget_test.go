package nzbget

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var methodPattern = regexp.MustCompile(`<methodName>(\w+)</methodName>`)

// fakeNZBGet answers the handful of methods the bot calls, using the same
// Hi/Lo split and signed low halves a real server sends.
func fakeNZBGet(t *testing.T, calls *[]string, bodies *[]string) *httptest.Server {
	t.Helper()

	responses := map[string]string{
		"version": `<value><string>21.1</string></value>`,
		"status": `<value><struct>
			<member><name>DownloadRate</name><value><i4>5242880</i4></value></member>
			<member><name>AverageDownloadRate</name><value><i4>3145728</i4></value></member>
			<member><name>UpTimeSec</name><value><i4>90061</i4></value></member>
			<member><name>TotalDiskSpaceHi</name><value><i4>0</i4></value></member>
			<member><name>TotalDiskSpaceLo</name><value><i4>-2147483648</i4></value></member>
			<member><name>FreeDiskSpaceHi</name><value><i4>1</i4></value></member>
			<member><name>FreeDiskSpaceLo</name><value><i4>0</i4></value></member>
		</struct></value>`,
		"listgroups": `<value><array><data><value><struct>
			<member><name>NZBID</name><value><i4>7</i4></value></member>
			<member><name>NZBName</name><value><string>Ubuntu &amp; Friends</string></value></member>
			<member><name>Status</name><value><string>DOWNLOADING</string></value></member>
			<member><name>FileSizeHi</name><value><i4>0</i4></value></member>
			<member><name>FileSizeLo</name><value><i4>-2147483648</i4></value></member>
			<member><name>DownloadedSizeHi</name><value><i4>0</i4></value></member>
			<member><name>DownloadedSizeLo</name><value><i4>1073741824</i4></value></member>
			<member><name>Health</name><value><i4>1000</i4></value></member>
		</struct></value></data></array></value>`,
		"history": `<value><array><data><value><struct>
			<member><name>NZBID</name><value><i4>3</i4></value></member>
			<member><name>Name</name><value><string>Old Thing</string></value></member>
		</struct></value></data></array></value>`,
		"append":    `<value><i4>99</i4></value>`,
		"editqueue": `<value><boolean>1</boolean></value>`,
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		method := methodPattern.FindStringSubmatch(body)[1]
		*calls = append(*calls, method)
		*bodies = append(*bodies, body)

		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param>`+
			responses[method]+`</param></params></methodResponse>`)
	}))
}

func TestClientAgainstFakeServer(t *testing.T) {
	var calls, bodies []string
	server := fakeNZBGet(t, &calls, &bodies)
	defer server.Close()

	client := New(server.URL)
	ctx := context.Background()

	version, err := client.Version(ctx)
	if err != nil || version != "21.1" {
		t.Fatalf("Version = %q, %v", version, err)
	}

	status, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	// A signed low half must come back as the unsigned 2 GiB it stands for,
	// not as a negative number.
	if status.TotalDiskSpace != 2147483648 {
		t.Errorf("TotalDiskSpace = %d, want 2147483648", status.TotalDiskSpace)
	}
	if status.FreeDiskSpace != 1<<32 {
		t.Errorf("FreeDiskSpace = %d, want %d", status.FreeDiskSpace, int64(1)<<32)
	}
	if status.DownloadRate != 5242880 || status.UpTimeSec != 90061 {
		t.Errorf("status = %+v", status)
	}

	groups, err := client.ListGroups(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("ListGroups = %+v, %v", groups, err)
	}
	if groups[0].NZBName != "Ubuntu & Friends" {
		t.Errorf("NZBName = %q, want the entity decoded", groups[0].NZBName)
	}
	if groups[0].FileSize != 2147483648 || groups[0].DownloadedSize != 1073741824 {
		t.Errorf("sizes = %d / %d", groups[0].DownloadedSize, groups[0].FileSize)
	}

	history, err := client.History(ctx, false)
	if err != nil || len(history) != 1 || history[0].Name != "Old Thing" {
		t.Fatalf("History = %+v, %v", history, err)
	}

	id, err := client.AppendURL(ctx, "http://example.invalid/nzb")
	if err != nil || id != 99 {
		t.Fatalf("AppendURL = %d, %v", id, err)
	}
	// Unpacking must be requested, or downloads land as unextracted archives.
	appendBody := bodies[len(bodies)-1]
	if !strings.Contains(appendBody, "*unpack:") || !strings.Contains(appendBody, "<string>yes</string>") {
		t.Errorf("append call did not request unpacking:\n%s", appendBody)
	}

	ok, err := client.EditQueue(ctx, "GroupFinalDelete", "", []int64{7})
	if err != nil || !ok {
		t.Fatalf("EditQueue = %v, %v", ok, err)
	}
	editBody := bodies[len(bodies)-1]
	if !strings.Contains(editBody, "<string>GroupFinalDelete</string>") || !strings.Contains(editBody, "<i4>7</i4>") {
		t.Errorf("editqueue call malformed:\n%s", editBody)
	}
}
