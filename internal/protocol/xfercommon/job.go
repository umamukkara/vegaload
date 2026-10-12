package xfercommon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Entry is one name from a directory listing. Time is UTC.
type Entry struct {
	Name string
	Type string
	Size int64
	Time time.Time
}

// Timing is how long the parts of a call took, in milliseconds.
type Timing struct {
	ConnectMs  int64
	LoginMs    int64
	TransferMs int64
	UploadMs   int64
	DownloadMs int64
	DeleteMs   int64
}

// Reply is what a file-transfer call returns besides the byte counts.
type Reply struct {
	Entries []Entry
	Size    int64
	Total   int
	Timing  Timing
	Text    string
}

// Job is the work of one call. Connection options are not part of it.
type Job struct {
	Mode          string
	Path          string
	Size          int64
	SizeSet       bool
	Fill          string
	ExpectSize    int64
	ExpectSizeSet bool
	ExpectSHA     string
	Expect        string
	Limit         int
	Hidden        bool
}

// ParseJob reads the job options. prefix is the protocol name in errors.
// isConn reports a connection option, which this function ignores.
// listDefault is the path used when mode is list and path is empty.
// rejectDashPath refuses a list path that starts with "-".
func ParseJob(prefix string, opts map[string]string, body []byte, isConn func(string) bool, listDefault string, rejectDashPath bool) (Job, error) {
	var job Job
	if len(body) > MaxBody {
		return job, fmt.Errorf("%s: the body is %d bytes, the most is %d", prefix, len(body), MaxBody)
	}
	job.Mode = opt(opts, "mode", "download")
	switch job.Mode {
	case "connect", "download", "upload", "list", "stat", "delete", "roundtrip":
	default:
		return job, fmt.Errorf("%s: mode=%q, want connect, download, upload, list, stat, delete, or roundtrip", prefix, job.Mode)
	}
	if err := rejectForeign(prefix, job.Mode, opts, body, isConn); err != nil {
		return job, err
	}
	job.Path = opt(opts, "path", "")
	if job.Mode == "list" && job.Path == "" {
		job.Path = listDefault
	}
	if job.Mode != "connect" {
		if err := CheckPath(job.Path); err != nil {
			return job, fmt.Errorf("%s: %w", prefix, err)
		}
	}
	if rejectDashPath && job.Mode == "list" && strings.HasPrefix(job.Path, "-") {
		return job, fmt.Errorf("%s: a list path must not start with -", prefix)
	}
	if v, ok := opts["size"]; ok {
		n, err := ParseSize(v)
		if err != nil {
			return job, fmt.Errorf("%s: %w", prefix, err)
		}
		job.Size = n
		job.SizeSet = true
	}
	job.Fill = opt(opts, "fill", "random")
	switch job.Fill {
	case "random", "zero", "text":
	default:
		return job, fmt.Errorf("%s: fill=%q, want random, zero, or text", prefix, job.Fill)
	}
	if v, ok := opts["expect_size"]; ok {
		n, err := ParseSize(v)
		if err != nil {
			return job, fmt.Errorf("%s: %w", prefix, err)
		}
		job.ExpectSize = n
		job.ExpectSizeSet = true
	}
	job.ExpectSHA = opt(opts, "expect_sha256", "")
	if job.ExpectSHA != "" && !ValidSHA256(job.ExpectSHA) {
		return job, fmt.Errorf("%s: expect_sha256 must be 64 hex characters", prefix)
	}
	job.Expect = opt(opts, "expect", "")
	limitText, limitSet := opts["limit"]
	limit := 1000
	if limitSet {
		n, err := strconv.Atoi(limitText)
		if err != nil {
			return job, fmt.Errorf("%s: option limit=%q: want a whole number", prefix, limitText)
		}
		limit = n
	}
	if limit < 1 || limit > 100000 {
		return job, fmt.Errorf("%s: limit must be from 1 to 100000", prefix)
	}
	job.Limit = limit
	hidden, err := parseBool(prefix, opts, "hidden", false)
	if err != nil {
		return job, err
	}
	job.Hidden = hidden
	if job.SizeSet && len(body) > 0 {
		return job, fmt.Errorf("%s: use size or a body, not both", prefix)
	}
	if (job.Mode == "upload" || job.Mode == "roundtrip") && !job.SizeSet && len(body) == 0 {
		return job, fmt.Errorf("%s: upload needs size or a body", prefix)
	}
	return job, nil
}

func opt(opts map[string]string, key, def string) string {
	if v, ok := opts[key]; ok {
		return v
	}
	return def
}

func parseBool(prefix string, opts map[string]string, key string, def bool) (bool, error) {
	v, ok := opts[key]
	if !ok {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: option %s=%q: want true or false", prefix, key, v)
	}
	return b, nil
}

func rejectForeign(prefix, mode string, opts map[string]string, body []byte, isConn func(string) bool) error {
	bodyModes := map[string]bool{"upload": true, "roundtrip": true}
	allowed := map[string]bool{"mode": true}
	add := func(keys ...string) {
		for _, k := range keys {
			allowed[k] = true
		}
	}
	switch mode {
	case "connect":
	case "download":
		add("path", "expect_size", "expect_sha256", "expect")
	case "upload", "roundtrip":
		add("path", "size", "fill")
	case "list":
		add("path", "expect", "limit", "hidden")
	case "stat", "delete":
		add("path")
	}
	for k := range opts {
		if isConn != nil && isConn(k) {
			continue
		}
		if !allowed[k] {
			return fmt.Errorf("%s: option %s is not used in mode %s", prefix, k, mode)
		}
	}
	if len(body) > 0 && !bodyModes[mode] {
		return fmt.Errorf("%s: a body is not used in mode %s", prefix, mode)
	}
	return nil
}

// Refuse applies the write and delete safety rules. A nil error means the
// call may use the network.
func Refuse(prefix, mode, path string, allowWrites, allowAdmin, script bool) error {
	switch mode {
	case "upload":
		if !allowWrites {
			return fmt.Errorf("%s: upload writes a file on the server: %s", prefix, flagHint(script, false))
		}
		if !strings.Contains(path, "{id}") && !allowAdmin {
			if script {
				return fmt.Errorf("%s: upload to a fixed path replaces that file: use {id} in the path, or pass allow_admin: true in the connection options", prefix)
			}
			return fmt.Errorf("%s: upload to a fixed path replaces that file: use {id} in the path, or set allow_admin=true as well as allow_writes=true", prefix)
		}
	case "roundtrip":
		if !strings.Contains(path, "{id}") {
			return fmt.Errorf("%s: roundtrip needs {id} in the path, so it never overwrites or deletes a file that was already there", prefix)
		}
		if !allowWrites {
			return fmt.Errorf("%s: upload writes a file on the server: %s", prefix, flagHint(script, false))
		}
	case "delete":
		if !allowWrites || !allowAdmin {
			if allowWrites && script {
				return fmt.Errorf("%s: delete needs allow_admin: pass allow_admin: true in the connection options", prefix)
			}
			if script {
				return fmt.Errorf("%s: delete needs allow_admin=true as well as allow_writes=true: pass allow_writes: true in the connection options", prefix)
			}
			return fmt.Errorf("%s: delete needs allow_admin=true as well as allow_writes=true", prefix)
		}
	}
	return nil
}

func flagHint(script, admin bool) string {
	if script {
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

// IDs makes a new {id} value for each call of one driver.
type IDs struct {
	salt string
	seq  atomic.Uint64
}

// NewIDs returns an id source. The salt is random.
func NewIDs() (*IDs, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	return &IDs{salt: hex.EncodeToString(b[:])}, nil
}

// Next returns the next id. It is unique for this source.
func (ids *IDs) Next() string {
	return ids.salt + "-" + strconv.FormatUint(ids.seq.Add(1), 10)
}

// Pending is the id the next Next call will return.
func (ids *IDs) Pending() string {
	return ids.salt + "-" + strconv.FormatUint(ids.seq.Load()+1, 10)
}

// ReadStream reads r to the end. It never keeps the whole stream.
// When expect is set, it looks for that text in the first 1 MiB.
// When hash is set, it returns the SHA-256 hex of the whole stream.
func ReadStream(r io.Reader, expect string, doHash bool) (int64, string, bool, error) {
	buf := make([]byte, 64*1024)
	var h io.Writer
	if doHash {
		h = sha256.New()
	}
	var n int64
	var win []byte
	found := expect == ""
	const capBytes = 1 << 20
	for {
		nr, err := r.Read(buf)
		if nr > 0 {
			chunk := buf[:nr]
			if h != nil {
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
	if hh, ok := h.(interface{ Sum([]byte) []byte }); ok {
		sum = HexDigest(hh.Sum(nil))
	}
	return n, sum, found, nil
}

// HashPayload is the SHA-256 hex of the bytes an upload would send.
func HashPayload(fill string, size int64, sizeSet bool, body []byte) string {
	h := sha256.New()
	if sizeSet {
		r, err := FillReader(fill, size)
		if err != nil {
			return ""
		}
		_, _ = io.Copy(h, r)
	} else {
		_, _ = h.Write(body)
	}
	return HexDigest(h.Sum(nil))
}

// Cut keeps at most n bytes of s.
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TextLeft is the reply text when a roundtrip file could not be deleted.
func TextLeft(path string) string {
	return fmt.Sprintf("the file %s is left on the server", path)
}
