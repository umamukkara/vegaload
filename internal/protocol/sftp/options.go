package sftp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"

	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
	"github.com/vegaload/vegaload/internal/urlerr"
)

func (d *Driver) parseURL() error {
	u, err := url.Parse(d.target.URL)
	if err != nil {
		return fmt.Errorf("sftp: parsing target URL: %v", urlerr.Inner(err))
	}
	if strings.ToLower(u.Scheme) != "sftp" {
		return fmt.Errorf("sftp: unsupported scheme %q, want sftp://", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("sftp: the target has no host")
	}
	d.host = u.Hostname()
	d.port = "22"
	if u.Port() != "" {
		d.port = u.Port()
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			return fmt.Errorf("sftp: do not put a password in the URL, use -opt password_env=NAME")
		}
		d.username = u.User.Username()
	}
	if q := u.Query(); len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sortStrings(keys)
		return fmt.Errorf("sftp: the URL query is not allowed (%s)", strings.Join(keys, ", "))
	}
	if u.EscapedPath() != "" && u.EscapedPath() != "/" {
		return fmt.Errorf("sftp: put the remote path in -opt path, not in the URL")
	}
	return nil
}

func (d *Driver) readConn(password, passphrase *string) error {
	opts := d.target.Options
	if err := xfercommon.CheckText("username", d.username); err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	urlUser := d.username
	if v, ok := opts["username"]; ok {
		if err := xfercommon.CheckText("username", v); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		if urlUser != "" && urlUser != v {
			return fmt.Errorf("sftp: username is set in the URL and in the options, and they differ")
		}
		d.username = v
	}
	if d.username == "" {
		return fmt.Errorf("sftp: a username is required")
	}

	if password != nil {
		if err := xfercommon.CheckText("password", *password); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		d.password = *password
	} else if name, ok := opts["password_env"]; ok {
		v, exists := os.LookupEnv(name)
		if !exists {
			return fmt.Errorf("sftp: password_env %s is not set", name)
		}
		if err := xfercommon.CheckText("password", v); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		d.password = v
	}

	if _, hasKey := opts["key_file"]; !hasKey {
		if _, ok := opts["key_passphrase_env"]; ok {
			return fmt.Errorf("sftp: key_passphrase_env needs key_file")
		}
		if passphrase != nil {
			return fmt.Errorf("sftp: key_passphrase needs key_file")
		}
	}
	var pass string
	var hasPass bool
	if passphrase != nil {
		if err := xfercommon.CheckText("passphrase", *passphrase); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		pass = *passphrase
		hasPass = true
		d.passphrase = pass
	} else if name, ok := opts["key_passphrase_env"]; ok {
		v, exists := os.LookupEnv(name)
		if !exists {
			return fmt.Errorf("sftp: key_passphrase_env %s is not set", name)
		}
		if err := xfercommon.CheckText("passphrase", v); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		pass = v
		hasPass = true
		d.passphrase = pass
	}
	if path, ok := opts["key_file"]; ok {
		signer, err := loadKey(path, pass, hasPass)
		if err != nil {
			return err
		}
		d.signer = signer
	}
	if d.password == "" && d.signer == nil {
		return fmt.Errorf("sftp: set password_env or key_file")
	}

	if v, ok := opts["host_key"]; ok {
		fps, err := parseFingerprints(v)
		if err != nil {
			return err
		}
		d.hostKeys = fps
	}
	d.insecure = d.target.InsecureSkipVerify
	if d.insecure && len(d.hostKeys) > 0 {
		return fmt.Errorf("sftp: host_key cannot be used with -insecure")
	}

	if v, ok := opts["connection"]; ok {
		switch v {
		case "shared":
			d.perCall = false
		case "per_call":
			d.perCall = true
		default:
			return fmt.Errorf("sftp: connection=%q, want shared or per_call", v)
		}
	}
	n, err := d.target.OptionInt("connections", 1)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	if n < 1 || n > 256 {
		return fmt.Errorf("sftp: connections must be from 1 to 256")
	}
	d.conns = n

	d.allowWrites, err = d.target.OptionBool("allow_writes", false)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	d.allowAdmin, err = d.target.OptionBool("allow_admin", false)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	if d.allowAdmin && !d.allowWrites {
		return fmt.Errorf("sftp: allow_admin=true needs allow_writes=true as well")
	}
	d.concurrentWrites, err = d.target.OptionBool("concurrent_writes", false)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	return nil
}

func (d *Driver) readJob() error {
	job, err := xfercommon.ParseJob("sftp", d.target.Options, d.target.Body, connOption, ".", false)
	if err != nil {
		return err
	}
	d.mode = job.Mode
	d.path = job.Path
	d.size = job.Size
	d.sizeSet = job.SizeSet
	d.fill = job.Fill
	d.expectSize = job.ExpectSize
	d.expectSizeSet = job.ExpectSizeSet
	d.expectSHA = job.ExpectSHA
	d.expect = job.Expect
	d.limit = job.Limit
	d.hidden = job.Hidden
	return nil
}

func connOption(k string) bool {
	switch k {
	case "username", "password_env", "key_file", "key_passphrase_env", "host_key",
		"connections", "connection", "allow_writes", "allow_admin", "concurrent_writes":
		return true
	}
	return false
}

// A signer is safe to share across goroutines. The cache key is the file
// path, its size and modification time, and a hash of the passphrase.
// The passphrase text is not stored. Failures are not cached.
type signerCacheKey struct {
	path    string
	size    int64
	modNano int64
	hasPass bool
	passSum string
}

var (
	signerMu    sync.Mutex
	signerCache = map[signerCacheKey]ssh.Signer{}
	signerLoads atomic.Int64
)

func loadKey(path, passphrase string, hasPass bool) (ssh.Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("sftp: key_file %s: cannot read", path)
	}
	id := signerCacheKey{
		path: path, size: info.Size(), modNano: info.ModTime().UnixNano(), hasPass: hasPass,
	}
	if hasPass {
		sum := sha256.Sum256([]byte(passphrase))
		id.passSum = hex.EncodeToString(sum[:])
	}
	signerMu.Lock()
	defer signerMu.Unlock()
	if s, ok := signerCache[id]; ok {
		return s, nil
	}
	signer, err := parseKeyFile(path, passphrase, hasPass)
	if err != nil {
		return nil, err
	}
	signerCache[id] = signer
	return signer, nil
}

func parseKeyFile(path, passphrase string, hasPass bool) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sftp: key_file %s: cannot read", path)
	}
	defer func() {
		for i := range b {
			b[i] = 0
		}
	}()
	signerLoads.Add(1)
	var signer ssh.Signer
	if hasPass {
		pw := []byte(passphrase)
		signer, err = ssh.ParsePrivateKeyWithPassphrase(b, pw)
		for i := range pw {
			pw[i] = 0
		}
	} else {
		signer, err = ssh.ParsePrivateKey(b)
	}
	if err != nil {
		return nil, keyErr(path, err, hasPass)
	}
	return signer, nil
}

func keyErr(path string, err error, hasPass bool) error {
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return fmt.Errorf("sftp: key_file %s: the key is encrypted: set key_passphrase_env", path)
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "passphrase") || strings.Contains(low, "decrypt") || strings.Contains(low, "password") {
		if hasPass {
			return fmt.Errorf("sftp: key_file %s: wrong passphrase", path)
		}
		return fmt.Errorf("sftp: key_file %s: the key is encrypted: set key_passphrase_env", path)
	}
	return fmt.Errorf("sftp: key_file %s: not a private key", path)
}

func parseFingerprints(s string) ([]string, error) {
	bad := errors.New("sftp: host_key must be SHA256: followed by 43 base64 characters")
	if strings.TrimSpace(s) == "" {
		return nil, bad
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if !validFingerprint(p) {
			return nil, bad
		}
		out = append(out, p)
	}
	return out, nil
}

func validFingerprint(s string) bool {
	rest, ok := strings.CutPrefix(s, "SHA256:")
	if !ok || len(rest) != 43 {
		return false
	}
	b, err := base64.RawStdEncoding.DecodeString(rest)
	return err == nil && len(b) == 32
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
