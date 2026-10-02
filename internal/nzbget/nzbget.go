// Package nzbget wraps the NZBGet XML-RPC API in typed calls.
package nzbget

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/xmlrpc"
)

// Client talks to one NZBGet instance.
type Client struct {
	rpc *xmlrpc.Client
}

// New returns a client for the given XML-RPC base URL.
func New(baseURL string) *Client {
	return &Client{rpc: xmlrpc.New(baseURL)}
}

// Group is one entry of the download queue.
//
// NZBGet reports every size as a Hi/Lo pair of 32-bit halves; they are joined
// here so callers see a single count of bytes.
type Group struct {
	NZBID             int64
	NZBName           string
	NZBNicename       string
	Status            string
	URL               string
	FileSize          int64
	DownloadedSize    int64
	RemainingSize     int64
	PostInfoText      string
	PostStageProgress int64
	PostStageTimeSec  int64
	Health            int64
}

// HistoryItem is one entry of the download history.
type HistoryItem struct {
	NZBID   int64
	Name    string
	NZBName string
}

// Status is the server-wide status snapshot.
type Status struct {
	DownloadRate        int64
	AverageDownloadRate int64
	UpTimeSec           int64
	TotalDiskSpace      int64
	FreeDiskSpace       int64
}

// Version returns the NZBGet version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	result, err := c.rpc.Call(ctx, "version")
	if err != nil {
		return "", err
	}
	return asString(result), nil
}

// Status returns the server status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	result, err := c.rpc.Call(ctx, "status")
	if err != nil {
		return Status{}, err
	}
	members, ok := result.(map[string]any)
	if !ok {
		return Status{}, fmt.Errorf("nzbget: unexpected status response %T", result)
	}
	return Status{
		DownloadRate:        asInt(members["DownloadRate"]),
		AverageDownloadRate: asInt(members["AverageDownloadRate"]),
		UpTimeSec:           asInt(members["UpTimeSec"]),
		TotalDiskSpace:      hiLo(members, "TotalDiskSpace"),
		FreeDiskSpace:       hiLo(members, "FreeDiskSpace"),
	}, nil
}

// ListGroups returns the current download queue.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	result, err := c.rpc.Call(ctx, "listgroups")
	if err != nil {
		return nil, err
	}
	items, ok := result.([]any)
	if !ok {
		return nil, fmt.Errorf("nzbget: unexpected listgroups response %T", result)
	}
	groups := make([]Group, 0, len(items))
	for _, item := range items {
		members, ok := item.(map[string]any)
		if !ok {
			continue
		}
		groups = append(groups, Group{
			NZBID:             asInt(members["NZBID"]),
			NZBName:           asString(members["NZBName"]),
			NZBNicename:       asString(members["NZBNicename"]),
			Status:            asString(members["Status"]),
			URL:               asString(members["URL"]),
			FileSize:          hiLo(members, "FileSize"),
			DownloadedSize:    hiLo(members, "DownloadedSize"),
			RemainingSize:     hiLo(members, "RemainingSize"),
			PostInfoText:      asString(members["PostInfoText"]),
			PostStageProgress: asInt(members["PostStageProgress"]),
			PostStageTimeSec:  asInt(members["PostStageTimeSec"]),
			Health:            asInt(members["Health"]),
		})
	}
	return groups, nil
}

// History returns the download history. Pass hidden to include hidden records.
func (c *Client) History(ctx context.Context, hidden bool) ([]HistoryItem, error) {
	result, err := c.rpc.Call(ctx, "history", hidden)
	if err != nil {
		return nil, err
	}
	items, ok := result.([]any)
	if !ok {
		return nil, fmt.Errorf("nzbget: unexpected history response %T", result)
	}
	history := make([]HistoryItem, 0, len(items))
	for _, item := range items {
		members, ok := item.(map[string]any)
		if !ok {
			continue
		}
		history = append(history, HistoryItem{
			NZBID:   asInt(members["NZBID"]),
			Name:    asString(members["Name"]),
			NZBName: asString(members["NZBName"]),
		})
	}
	return history, nil
}

// appendCall submits an NZB and returns the assigned NZBID. Unpacking is
// requested through the post-processing parameters so the archive is extracted
// once the download completes.
func (c *Client) appendCall(ctx context.Context, filename, content string) (int64, error) {
	result, err := c.rpc.Call(ctx, "append",
		filename,                        // NZBFilename
		content,                         // Content: base64 of the NZB, or a URL to fetch
		"",                              // Category
		0,                               // Priority
		false,                           // AddToTop
		false,                           // AddPaused
		"",                              // DupeKey
		0,                               // DupeScore
		"SCORE",                         // DupeMode
		false,                           // trailing flag carried over from the Python bot's call
		[]any{[]any{"*unpack:", "yes"}}, // PPParameters
	)
	if err != nil {
		return 0, err
	}
	id := asInt(result)
	if id <= 0 {
		return 0, fmt.Errorf("nzbget: append rejected %q", filename)
	}
	return id, nil
}

// AppendFile submits a local .nzb file. NZBGet names the download after
// name, not path, so the file can live at a temp path without the queue
// showing it.
func (c *Client) AppendFile(ctx context.Context, path, name string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return c.appendCall(ctx, name, base64.StdEncoding.EncodeToString(raw))
}

// AppendURL asks NZBGet to fetch the NZB itself.
func (c *Client) AppendURL(ctx context.Context, url string) (int64, error) {
	return c.appendCall(ctx, "", url)
}

// EditQueue runs an edit-queue command against the given NZBIDs.
func (c *Client) EditQueue(ctx context.Context, command, param string, ids []int64) (bool, error) {
	result, err := c.rpc.Call(ctx, "editqueue", command, param, ids)
	if err != nil {
		return false, err
	}
	ok, _ := result.(bool)
	return ok, nil
}

// hiLo joins one of NZBGet's split size fields. The low half is documented as
// the low 32 bits but travels as a signed i4, so it arrives negative once a
// size crosses 2 GiB; masking back to uint32 restores the intended value.
func hiLo(members map[string]any, prefix string) int64 {
	return asInt(members[prefix+"Hi"])<<32 | int64(uint32(asInt(members[prefix+"Lo"])))
}

func asInt(v any) int64 {
	switch value := v.(type) {
	case int64:
		return value
	case float64:
		return int64(value)
	case bool:
		if value {
			return 1
		}
	}
	return 0
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
