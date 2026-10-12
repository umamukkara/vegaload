package sftp

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

const (
	modeConnect   = "connect"
	modeDownload  = "download"
	modeUpload    = "upload"
	modeList      = "list"
	modeStat      = "stat"
	modeDelete    = "delete"
	modeRoundtrip = "roundtrip"
)

func (d *Driver) download(l *live, path string) (int64, xfercommon.Reply, error) {
	fi, err := l.cli.Stat(path)
	if err != nil {
		return 0, xfercommon.Reply{}, err
	}
	if fi.IsDir() {
		return 0, xfercommon.Reply{}, fmt.Errorf("sftp: %s is a directory", path)
	}
	f, err := l.cli.Open(path)
	if err != nil {
		return 0, xfercommon.Reply{}, err
	}
	sw := &scan{expect: d.expect, found: d.expect == ""}
	if d.expectSHA != "" {
		sw.h = sha256.New()
	}
	_, rerr := io.Copy(sw, f)
	cerr := f.Close()
	var rep xfercommon.Reply
	rep.Size = sw.n
	if rerr != nil {
		return sw.n, rep, rerr
	}
	if cerr != nil {
		return sw.n, rep, cerr
	}
	if d.expectSizeSet && sw.n != d.expectSize {
		return sw.n, rep, fmt.Errorf("sftp: the file is %d bytes, want %d", sw.n, d.expectSize)
	}
	if d.expectSHA != "" && !strings.EqualFold(sw.sum(), d.expectSHA) {
		return sw.n, rep, fmt.Errorf("sftp: the SHA-256 does not match")
	}
	if d.expect != "" && !sw.found {
		return sw.n, rep, fmt.Errorf("sftp: the file does not contain the expected text")
	}
	rep.Text = fmt.Sprintf("received %d bytes", sw.n)
	return sw.n, rep, nil
}

func (d *Driver) upload(l *live, path string, body []byte) (int64, xfercommon.Reply, error) {
	f, err := l.cli.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, xfercommon.Reply{}, err
	}
	var want int64
	var r io.Reader
	if d.sizeSet {
		fr, ferr := xfercommon.FillReader(d.fill, d.size)
		if ferr != nil {
			_ = f.Close()
			return 0, xfercommon.Reply{}, fmt.Errorf("sftp: %w", ferr)
		}
		r = fr
		want = d.size
	} else {
		r = bytes.NewReader(body)
		want = int64(len(body))
	}
	cr := &xfercommon.CountingReader{R: r}
	var werr error
	if want > 0 {
		// The sftp package uses concurrent writes only when the reader
		// says how much it has left. A CountingReader does not say that.
		_, werr = io.Copy(f, &sizedReader{Reader: cr, size: want})
	}
	cerr := f.Close()
	rep := xfercommon.Reply{Size: cr.N}
	if werr != nil {
		return cr.N, rep, werr
	}
	if cerr != nil {
		return cr.N, rep, cerr
	}
	rep.Text = fmt.Sprintf("sent %d bytes", cr.N)
	return cr.N, rep, nil
}

func (d *Driver) list(l *live, path string) (xfercommon.Reply, error) {
	entries, err := l.cli.ReadDir(path)
	if err != nil {
		low := strings.ToLower(err.Error())
		// Some servers answer "no such file" when the path is a file.
		if errors.Is(err, os.ErrNotExist) {
			if fi, serr := l.cli.Stat(path); serr == nil && !fi.IsDir() {
				return xfercommon.Reply{}, fmt.Errorf("sftp: %s is not a directory", path)
			}
		}
		if strings.Contains(low, "not a directory") {
			return xfercommon.Reply{}, fmt.Errorf("sftp: %s is not a directory", path)
		}
		return xfercommon.Reply{}, err
	}
	filtered := entries[:0]
	for _, e := range entries {
		if !d.hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		filtered = append(filtered, e)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name() < filtered[j].Name() })
	found := d.expect == ""
	for _, e := range filtered {
		if e.Name() == d.expect {
			found = true
			break
		}
	}
	rep := xfercommon.Reply{Total: len(filtered), Text: fmt.Sprintf("%d entries", len(filtered))}
	n := d.limit
	if n > len(filtered) {
		n = len(filtered)
	}
	rep.Entries = make([]xfercommon.Entry, 0, n)
	for _, e := range filtered[:n] {
		rep.Entries = append(rep.Entries, xfercommon.Entry{
			Name: e.Name(),
			Type: kind(e.Mode()),
			Size: e.Size(),
			Time: e.ModTime().UTC(),
		})
	}
	if !found {
		return rep, fmt.Errorf("sftp: the listing does not contain %s", d.expect)
	}
	return rep, nil
}

func (d *Driver) stat(l *live, path string) (xfercommon.Reply, error) {
	fi, err := l.cli.Stat(path)
	if err != nil {
		return xfercommon.Reply{}, err
	}
	typ := kind(fi.Mode())
	rep := xfercommon.Reply{
		Size: fi.Size(),
		Text: fmt.Sprintf("size %d type %s", fi.Size(), typ),
		Entries: []xfercommon.Entry{{
			Name: path,
			Type: typ,
			Size: fi.Size(),
			Time: fi.ModTime().UTC(),
		}},
	}
	return rep, nil
}

func (d *Driver) remove(l *live, path string) error {
	fi, err := l.cli.Lstat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("sftp: delete removes files only, %s is a directory", path)
	}
	// Remove also removes an empty directory. The Lstat above refuses a
	// directory first. A swap between the two calls can still remove one.
	return l.cli.Remove(path)
}

func (d *Driver) roundtrip(l *live, path string, body []byte) (int64, int64, xfercommon.Reply, error) {
	var rep xfercommon.Reply
	upStart := time.Now()
	sent, _, err := d.upload(l, path, body)
	rep.Timing.UploadMs = time.Since(upStart).Milliseconds()
	if err != nil {
		rep = d.cleanup(l, path, rep)
		return sent, 0, rep, err
	}
	downStart := time.Now()
	f, err := l.cli.Open(path)
	if err != nil {
		rep.Timing.DownloadMs = time.Since(downStart).Milliseconds()
		rep = d.cleanup(l, path, rep)
		return sent, 0, rep, err
	}
	sw := &scan{h: sha256.New(), found: true}
	_, rerr := io.Copy(sw, f)
	cerr := f.Close()
	rep.Timing.DownloadMs = time.Since(downStart).Milliseconds()
	got := sw.n
	if rerr != nil {
		rep = d.cleanup(l, path, rep)
		return sent, got, rep, rerr
	}
	if cerr != nil {
		rep = d.cleanup(l, path, rep)
		return sent, got, rep, cerr
	}
	want := xfercommon.HashPayload(d.fill, d.size, d.sizeSet, body)
	if got != sent || !strings.EqualFold(sw.sum(), want) {
		rep = d.cleanup(l, path, rep)
		return sent, got, rep, fmt.Errorf("sftp: the downloaded file is not the file that was uploaded (%d bytes sent, %d received)", sent, got)
	}
	rep = d.cleanup(l, path, rep)
	if rep.Text != "" {
		return sent, got, rep, nil
	}
	rep.Text = "uploaded, downloaded and deleted"
	rep.Size = got
	return sent, got, rep, nil
}

func (d *Driver) cleanup(l *live, path string, rep xfercommon.Reply) xfercommon.Reply {
	if l.dead.Load() {
		rep.Text = xfercommon.TextLeft(path)
		return rep
	}
	start := time.Now()
	if l.raw != nil {
		_ = l.raw.SetDeadline(time.Now().Add(time.Second))
	}
	fi, err := l.cli.Lstat(path)
	switch {
	case err != nil && errors.Is(err, os.ErrNotExist):
		// The upload never created a file.
	case err != nil:
		rep.Text = xfercommon.TextLeft(path)
		if isLost(err) {
			l.kill()
		}
	case fi.IsDir():
		// Remove also removes an empty directory. Leave the directory in place.
		rep.Text = xfercommon.TextLeft(path)
	default:
		err = l.cli.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			rep.Text = xfercommon.TextLeft(path)
			if isLost(err) {
				l.kill()
			}
		}
	}
	if l.raw != nil && !l.dead.Load() {
		_ = l.raw.SetDeadline(time.Time{})
	}
	rep.Timing.DeleteMs = time.Since(start).Milliseconds()
	return rep
}

// sizedReader tells the sftp package the upload size. With concurrent_writes
// on, that is what lets File.ReadFrom send several writes at once.
type sizedReader struct {
	io.Reader
	size int64
}

func (r *sizedReader) Size() int64 { return r.size }

func kind(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "link"
	case m.IsDir():
		return "dir"
	case m.IsRegular():
		return "file"
	default:
		return "other"
	}
}

// scan counts a download. It hashes when h is set, and it looks for
// expect in the first 1 MiB. It does not keep the file.
type scan struct {
	n      int64
	h      hash.Hash
	win    []byte
	found  bool
	expect string
}

func (s *scan) Write(p []byte) (int, error) {
	if s.h != nil {
		_, _ = s.h.Write(p)
	}
	const capBytes = 1 << 20
	if !s.found && s.n < capBytes {
		take := p
		if s.n+int64(len(take)) > capBytes {
			take = take[:capBytes-s.n]
		}
		s.win = append(s.win, take...)
		if strings.Contains(string(s.win), s.expect) {
			s.found = true
			s.win = nil
		} else if len(s.win) > len(s.expect)+len(take) {
			s.win = append([]byte(nil), s.win[len(s.win)-len(s.expect):]...)
		}
	}
	s.n += int64(len(p))
	return len(p), nil
}

func (s *scan) sum() string {
	if s.h == nil {
		return ""
	}
	return xfercommon.HexDigest(s.h.Sum(nil))
}
