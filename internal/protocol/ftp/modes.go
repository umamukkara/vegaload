package ftp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

func (d *Driver) refuse() error {
	return xfercommon.Refuse("ftp", d.mode, d.path, d.allowWrites, d.allowAdmin, d.script)
}

func (d *Driver) download(s *session, path string) (int64, Reply, error) {
	resp, err := s.srv.Retr(path)
	if err != nil {
		return 0, Reply{}, err
	}
	defer func() { _ = resp.Close() }()
	var rep Reply
	n, sum, found, err := xfercommon.ReadStream(resp, d.expect, d.expectSHA != "")
	rep.Size = n
	if err != nil {
		return n, rep, err
	}
	if cerr := resp.Close(); cerr != nil && err == nil {
		return n, rep, cerr
	}
	if d.expectSizeSet && n != d.expectSize {
		return n, rep, fmt.Errorf("ftp: the file is %d bytes, want %d", n, d.expectSize)
	}
	if d.expectSHA != "" && !strings.EqualFold(sum, d.expectSHA) {
		return n, rep, fmt.Errorf("ftp: the SHA-256 does not match")
	}
	if d.expect != "" && !found {
		return n, rep, fmt.Errorf("ftp: the file does not contain the expected text")
	}
	rep.Text = fmt.Sprintf("received %d bytes", n)
	return n, rep, nil
}

func (d *Driver) upload(s *session, path string, body []byte) (int64, Reply, error) {
	var r io.Reader
	var n int64
	if d.sizeSet {
		fr, err := xfercommon.FillReader(d.fill, d.size)
		if err != nil {
			return 0, Reply{}, fmt.Errorf("ftp: %w", err)
		}
		r = fr
		n = d.size
	} else {
		r = bytes.NewReader(body)
		n = int64(len(body))
	}
	cr := &xfercommon.CountingReader{R: r}
	if err := s.srv.Stor(path, cr); err != nil {
		return cr.N, Reply{}, err
	}
	return cr.N, Reply{Text: fmt.Sprintf("sent %d bytes", n), Size: cr.N}, nil
}

func (d *Driver) list(s *session, path string) (int64, Reply, error) {
	entries, err := s.srv.List(path)
	if err != nil {
		return s.readN.Load(), Reply{}, err
	}
	rep := Reply{Total: len(entries), Text: fmt.Sprintf("%d entries", len(entries))}
	limit := d.limit
	if limit > len(entries) {
		limit = len(entries)
	}
	rep.Entries = make([]Entry, 0, limit)
	found := d.expect == ""
	for i, e := range entries {
		if e.Name == d.expect {
			found = true
		}
		if i >= limit {
			continue
		}
		rep.Entries = append(rep.Entries, Entry{
			Name: e.Name,
			Type: entryType(e.Type),
			Size: int64(e.Size),
			Time: e.Time.UTC(),
		})
	}
	if !found {
		return s.readN.Load(), rep, fmt.Errorf("ftp: the listing does not contain %s", d.expect)
	}
	return s.readN.Load(), rep, nil
}

func (d *Driver) stat(s *session, path string) (Reply, error) {
	n, err := s.srv.FileSize(path)
	if err != nil {
		if strings.Contains(err.Error(), "502") || strings.Contains(strings.ToLower(err.Error()), "not supported") {
			return Reply{}, fmt.Errorf("ftp: the server does not support SIZE: %s", err.Error())
		}
		return Reply{}, err
	}
	rep := Reply{Size: n, Text: fmt.Sprintf("size %d", n)}
	if s.srv.IsGetTimeSupported() {
		if t, err := s.srv.GetTime(path); err == nil {
			rep.Entries = []Entry{{Name: path, Type: "file", Size: n, Time: t.UTC()}}
		}
	}
	return rep, nil
}

func (d *Driver) roundtrip(ctx context.Context, s *session, path string, body []byte) (int64, int64, Reply, error) {
	var rep Reply
	upStart := time.Now()
	sent, _, err := d.upload(s, path, body)
	rep.Timing.UploadMs = time.Since(upStart).Milliseconds()
	if err != nil {
		rep = d.cleanup(s, path, rep)
		return sent, 0, rep, err
	}
	downStart := time.Now()
	resp, err := s.srv.Retr(path)
	if err != nil {
		rep.Timing.DownloadMs = time.Since(downStart).Milliseconds()
		rep = d.cleanup(s, path, rep)
		return sent, 0, rep, err
	}
	got, sum, _, rerr := xfercommon.ReadStream(resp, "", true)
	cerr := resp.Close()
	rep.Timing.DownloadMs = time.Since(downStart).Milliseconds()
	if rerr != nil {
		rep = d.cleanup(s, path, rep)
		return sent, got, rep, rerr
	}
	if cerr != nil {
		rep = d.cleanup(s, path, rep)
		return sent, got, rep, cerr
	}
	want := xfercommon.HashPayload(d.fill, d.size, d.sizeSet, body)
	if got != sent || !strings.EqualFold(sum, want) {
		rep = d.cleanup(s, path, rep)
		return sent, got, rep, fmt.Errorf("ftp: the downloaded file is not the file that was uploaded (%d bytes sent, %d received)", sent, got)
	}
	rep = d.cleanup(s, path, rep)
	if rep.Text != "" {
		return sent, got, rep, nil
	}
	rep.Text = "uploaded, downloaded and deleted"
	rep.Size = got
	_ = ctx
	return sent, got, rep, nil
}

func (d *Driver) cleanup(s *session, path string, rep Reply) Reply {
	start := time.Now()
	cctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if dl, ok := cctx.Deadline(); ok && s.ctrl != nil {
		_ = s.ctrl.SetDeadline(dl)
	}
	err := s.srv.Delete(path)
	rep.Timing.DeleteMs = time.Since(start).Milliseconds()
	if err != nil {
		rep.Text = xfercommon.TextLeft(path)
	}
	return rep
}

func entryType(t ftp.EntryType) string {
	switch t {
	case ftp.EntryTypeFolder:
		return "dir"
	case ftp.EntryTypeLink:
		return "link"
	default:
		return "file"
	}
}
