package ftp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

func (d *Driver) refuse() error {
	switch d.mode {
	case modeUpload:
		if !d.allowWrites {
			return fmt.Errorf("ftp: upload writes a file on the server: %s", d.flagHint(false))
		}
		if !strings.Contains(d.path, "{id}") && !d.allowAdmin {
			if d.script {
				return fmt.Errorf("ftp: upload to a fixed path replaces that file: use {id} in the path, or pass allow_admin: true in the connection options")
			}
			return fmt.Errorf("ftp: upload to a fixed path replaces that file: use {id} in the path, or set allow_admin=true as well as allow_writes=true")
		}
	case modeRoundtrip:
		if !strings.Contains(d.path, "{id}") {
			return fmt.Errorf("ftp: roundtrip needs {id} in the path, so it never overwrites or deletes a file that was already there")
		}
		if !d.allowWrites {
			return fmt.Errorf("ftp: upload writes a file on the server: %s", d.flagHint(false))
		}
	case modeDelete:
		if !d.allowWrites || !d.allowAdmin {
			if d.allowWrites && d.script {
				return fmt.Errorf("ftp: delete needs allow_admin: pass allow_admin: true in the connection options")
			}
			if d.script {
				return fmt.Errorf("ftp: delete needs allow_admin=true as well as allow_writes=true: pass allow_writes: true in the connection options")
			}
			return fmt.Errorf("ftp: delete needs allow_admin=true as well as allow_writes=true")
		}
	}
	return nil
}

func (d *Driver) flagHint(admin bool) string {
	if d.script {
		if admin {
			return "pass allow_admin: true in the connection options"
		}
		return "pass allow_writes: true in the connection options"
	}
	if admin {
		return "use -opt allow_admin=true to allow it"
	}
	return "use -opt allow_writes=true to allow it"
}

func (d *Driver) download(s *session, path string) (int64, Reply, error) {
	resp, err := s.srv.Retr(path)
	if err != nil {
		return 0, Reply{}, err
	}
	defer func() { _ = resp.Close() }()
	var rep Reply
	n, sum, found, err := readStream(resp, d.expect, d.expectSHA != "")
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
	got, sum, _, rerr := readStream(resp, "", true)
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
	want := hashPayload(d, body)
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

func hashPayload(d *Driver, body []byte) string {
	h := sha256.New()
	if d.sizeSet {
		r, err := xfercommon.FillReader(d.fill, d.size)
		if err != nil {
			return ""
		}
		_, _ = io.Copy(h, r)
	} else {
		_, _ = h.Write(body)
	}
	return xfercommon.HexDigest(h.Sum(nil))
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
		rep.Text = fmt.Sprintf("the file %s is left on the server", path)
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

func readStream(r io.Reader, expect string, hash bool) (int64, string, bool, error) {
	buf := make([]byte, 64*1024)
	h := sha256.New()
	var n int64
	var win []byte
	found := expect == ""
	const capBytes = 1 << 20
	for {
		nr, err := r.Read(buf)
		if nr > 0 {
			chunk := buf[:nr]
			if hash {
				_, _ = h.Write(chunk)
			}
			if !found && n < capBytes {
				take := chunk
				if n+int64(len(take)) > capBytes {
					take = take[:capBytes-n]
				}
				win = append(win, take...)
				if strings.Contains(string(win), expect) {
					found = true
					win = nil
				} else if len(win) > len(expect)+len(take) {
					win = append([]byte(nil), win[len(win)-len(expect):]...)
				}
			}
			n += int64(nr)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, "", found, err
		}
	}
	sum := ""
	if hash {
		sum = xfercommon.HexDigest(h.Sum(nil))
	}
	return n, sum, found, nil
}
