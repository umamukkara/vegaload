package har

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// A slot is one recorded value that might be read from an earlier response.
type slot struct {
	req       int
	val       string
	field     string
	why       string
	page      bool
	secret    bool
	envName   string
	quiet     bool // a cookie or Authorization: no TODO when it is not carried
	bound     *binding
	otherHost string
}

type binding struct {
	src   int
	expr  string
	field string
	cname string
}

type cookiePair struct {
	name string
	raw  string
	slot int // 0, or one past the slot index
}

type emit struct {
	name string
	expr string
}

// pending is a JSON value whose slot is resolved after correlation.
type pending struct {
	slot int
	num  bool
	raw  string
}

type placeKind int

const (
	placeJSON placeKind = iota
	placeHTML
	placeCookie
	placeHeader
)

type step struct {
	key   string
	idx   int
	isIdx bool
}

type place struct {
	kind   placeKind
	field  string
	header string
	path   []step
}

var identRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func normName(name string) string {
	n := strings.ToLower(name)
	return strings.NewReplacer("-", "", "_", "", " ", "", ".", "").Replace(n)
}

// neverCarry is a value the user knows and types, even when a response echoes it.
func neverCarry(name string) bool {
	n := normName(name)
	switch n {
	case "pwd", "pass", "pin", "otp":
		return true
	}
	for _, s := range []string{"password", "passwd", "secret", "apikey", "credential", "privatekey", "accesskey"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// pageValue is a short name a page returns (a CSRF token, a view state).
func pageValue(name string) bool {
	n := normName(name)
	if n == "token" {
		return true
	}
	for _, s := range []string{"csrf", "xsrf", "viewstate", "eventvalidation", "authenticitytoken", "requestverificationtoken"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// consider records val when it should be carried or, if that fails, noted.
// The returned index is into b.slots.
func (b *builder) consider(field, val string) (int, bool) {
	if val == "" || neverCarry(field) {
		return 0, false
	}
	page := field != "" && pageValue(field)
	why := dynamicKind(val)
	jwt := reJWT.MatchString(val)
	if !page && why == "" && !jwt {
		return 0, false
	}
	if why == "" && jwt {
		why = "a JWT"
	}
	if why == "" {
		why = "a value the page returns"
	}
	s := slot{
		req: b.cur, val: val, field: field, why: why, page: page,
		secret: !page && (jwt || (field != "" && isSecretName(field))), envName: field,
	}
	if field == "" {
		s.field = "path"
		s.secret = jwt
		s.envName = "PATH"
	}
	b.slots = append(b.slots, s)
	return len(b.slots) - 1, true
}

func cookieValue(hs []nameValue) string {
	for _, h := range hs {
		if strings.EqualFold(h.Name, "cookie") {
			return h.Value
		}
	}
	return ""
}

func (b *builder) cookieSlots(raw string) []cookiePair {
	if raw == "" {
		return nil
	}
	var out []cookiePair
	for _, p := range strings.Split(raw, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, val, ok := strings.Cut(p, "=")
		name = strings.TrimSpace(name)
		cp := cookiePair{name: name, raw: p}
		if ok {
			if id, yes := b.consider(name, strings.TrimSpace(val)); yes {
				b.slots[id].quiet = true
				b.slots[id].secret = false
				cp.slot = id + 1
			}
		}
		out = append(out, cp)
	}
	return out
}

func (b *builder) authHeader(name, val string) header {
	token := val
	prefix := ""
	if len(val) > 7 && strings.EqualFold(val[:7], "bearer ") {
		prefix = val[:7]
		token = val[7:]
	}
	if id, ok := b.consider("Authorization", token); ok {
		b.slots[id].quiet = true
		b.slots[id].secret = true
		b.slots[id].envName = "Authorization"
		h := header{name: name, auth: true}
		if prefix != "" {
			h.val = append(h.val, part{lit: prefix})
		}
		h.val = append(h.val, part{slot: id + 1, lit: token})
		return h
	}
	return header{name: name, val: []part{b.secret(name)}}
}

func correlate(reqs []*request, slots []slot) {
	bindings := map[string]*binding{}
	for i := range slots {
		s := &slots[i]
		var hit *place
		var src int
		other := ""
		for j := s.req - 1; j >= 0; j-- {
			places := placesIn(reqs[j], s.val)
			if len(places) != 1 {
				continue
			}
			if strings.EqualFold(reqs[j].host, reqs[s.req].host) {
				p := places[0]
				hit = &p
				src = j
				break
			}
			if other == "" {
				other = reqs[j].host
			}
		}
		if hit == nil {
			s.otherHost = other
			continue
		}
		expr := hit.expr(src + 1)
		key := fmt.Sprintf("%d\x00%s", src, expr)
		bn, ok := bindings[key]
		if !ok {
			bn = &binding{src: src, expr: expr, field: hit.field}
			bindings[key] = bn
		}
		s.bound = bn
	}
	assignNames(slots)
}

func assignNames(slots []slot) {
	used := map[string]bool{}
	for i := range slots {
		bn := slots[i].bound
		if bn == nil || bn.cname != "" {
			continue
		}
		base := fmt.Sprintf("c%d_%s", bn.src+1, constField(bn.field))
		name := base
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		used[name] = true
		bn.cname = name
	}
}

func constField(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s = strings.Trim(b.String(), "_")
	if s == "" {
		return "value"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "v" + s
	}
	return s
}

func (p place) expr(n int) string {
	switch p.kind {
	case placeJSON:
		return jsonExpr(n, p.path)
	case placeHTML:
		return fmt.Sprintf("hidden(r%d.body, %s)", n, jsString(p.field))
	case placeCookie:
		return fmt.Sprintf("cookie(header(r%d, %s), %s)", n, jsString(p.header), jsString(p.field))
	default:
		return fmt.Sprintf("header(r%d, %s)", n, jsString(p.header))
	}
}

func jsonExpr(n int, path []step) string {
	e := fmt.Sprintf("r%d.json()", n)
	for _, s := range path {
		if s.isIdx {
			e += fmt.Sprintf("[%d]", s.idx)
			continue
		}
		if identRE.MatchString(s.key) {
			e += "." + s.key
		} else {
			e += "[" + jsString(s.key) + "]"
		}
	}
	return e
}

func placesIn(q *request, val string) []place {
	if val == "" {
		return nil
	}
	var out []place
	body := strings.TrimSpace(q.answer)
	if body != "" && strings.Contains(body, val) {
		if v, err := parseOrdered([]byte(body)); err == nil {
			collectJSON(v, nil, val, &out)
		} else if looksHTML(body) {
			collectHTML(body, val, &out)
		}
	}
	for _, h := range q.respHeaders {
		if strings.EqualFold(h.Name, "Set-Cookie") {
			name, cv, ok := splitCookie(h.Value)
			if ok && cv == val {
				out = append(out, place{kind: placeCookie, field: name, header: h.Name})
			}
			continue
		}
		if h.Value == val {
			out = append(out, place{kind: placeHeader, field: h.Name, header: h.Name})
		}
	}
	return out
}

func splitCookie(v string) (name, val string, ok bool) {
	nv, _, _ := strings.Cut(v, ";")
	name, val, ok = strings.Cut(strings.TrimSpace(nv), "=")
	name = strings.TrimSpace(name)
	return name, strings.TrimSpace(val), ok && name != ""
}

func looksHTML(s string) bool {
	t := strings.ToLower(s)
	return strings.Contains(t, "<input") || strings.Contains(t, "<html") || strings.Contains(t, "<form")
}

func collectJSON(v any, path []step, val string, out *[]place) {
	switch x := v.(type) {
	case jObj:
		for _, kv := range x {
			next := append(append([]step{}, path...), step{key: kv.key})
			collectJSON(kv.val, next, val, out)
		}
	case jArr:
		for i, e := range x {
			next := append(append([]step{}, path...), step{idx: i, isIdx: true})
			collectJSON(e, next, val, out)
		}
	case string:
		if x == val {
			*out = append(*out, place{kind: placeJSON, path: append([]step{}, path...), field: lastKey(path)})
		}
	case json.Number:
		if x.String() == val {
			*out = append(*out, place{kind: placeJSON, path: append([]step{}, path...), field: lastKey(path)})
		}
	}
}

func lastKey(path []step) string {
	for i := len(path) - 1; i >= 0; i-- {
		if !path[i].isIdx && path[i].key != "" {
			return path[i].key
		}
	}
	return "value"
}

func collectHTML(body, val string, out *[]place) {
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tn, _ := z.TagName()
		if !strings.EqualFold(string(tn), "input") {
			continue
		}
		var name, id, value string
		for {
			k, v, more := z.TagAttr()
			switch strings.ToLower(string(k)) {
			case "name":
				name = string(v)
			case "id":
				id = string(v)
			case "value":
				value = string(v)
			}
			if !more {
				break
			}
		}
		if value != val {
			continue
		}
		field := name
		if field == "" {
			field = id
		}
		if field == "" {
			continue
		}
		*out = append(*out, place{kind: placeHTML, field: field})
	}
}

func applySlots(reqs []*request, b *builder) {
	noteSlots(reqs, b.slots)
	seen := map[*binding]bool{}
	for _, s := range b.slots {
		if s.bound == nil || seen[s.bound] {
			continue
		}
		seen[s.bound] = true
		reqs[s.bound.src].emits = append(reqs[s.bound.src].emits, emit{s.bound.cname, s.bound.expr})
	}
	for _, q := range reqs {
		for i := range q.url {
			applyPart(&q.url[i], b.slots, b.env)
		}
		for i := range q.headers {
			h := &q.headers[i]
			switch {
			case h.auth:
				applyAuth(h, b.slots, b.env)
			case h.cookie:
				h.val = finishCookie(q.cookies, b.slots, b.env)
			default:
				for j := range h.val {
					applyPart(&h.val[j], b.slots, b.env)
				}
			}
		}
		if q.isJSON {
			q.jsonVal = resolveJSON(q.jsonVal, b.slots, b.env)
		}
		for i := range q.text {
			applyPart(&q.text[i], b.slots, b.env)
		}
		q.answer = ""
	}
}

func noteSlots(reqs []*request, slots []slot) {
	seen := map[string]bool{}
	for _, s := range slots {
		if s.bound != nil || s.quiet || s.secret {
			continue
		}
		note := noteFor(s)
		key := fmt.Sprintf("%d\x00%s", s.req, note)
		if seen[key] {
			continue
		}
		seen[key] = true
		reqs[s.req].notes = append(reqs[s.req].notes, note)
	}
}

func noteFor(s slot) string {
	if s.otherHost != "" {
		return fmt.Sprintf("%s was in an answer from %s, a different host, so it was left as recorded.", shorten(s.val), s.otherHost)
	}
	if s.page {
		return fmt.Sprintf("%s was not in an earlier answer on this host.", s.field)
	}
	return fmt.Sprintf("%s looks like %s.", shorten(s.val), s.why)
}

func applyPart(p *part, slots []slot, env map[string]bool) {
	if p.slot == 0 {
		return
	}
	s := &slots[p.slot-1]
	if s.bound != nil {
		p.expr = s.bound.cname
		if p.encode {
			p.expr = "encodeURIComponent(" + p.expr + ")"
		}
		return
	}
	if s.secret {
		n := envName(s.envName)
		env[n] = true
		p.expr = "env." + n
		if p.encode {
			p.expr = "encodeURIComponent(" + p.expr + ")"
		}
	}
}

func applyAuth(h *header, slots []slot, env map[string]bool) {
	id := 0
	for _, p := range h.val {
		if p.slot != 0 {
			id = p.slot
		}
	}
	if id == 0 {
		return
	}
	if slots[id-1].bound == nil {
		env["VL_AUTHORIZATION"] = true
		h.val = []part{{expr: "env.VL_AUTHORIZATION"}}
		return
	}
	for i := range h.val {
		applyPart(&h.val[i], slots, env)
	}
}

func finishCookie(pairs []cookiePair, slots []slot, env map[string]bool) []part {
	type carried struct{ name, cname string }
	var bound []carried
	for _, p := range pairs {
		if p.slot > 0 && slots[p.slot-1].bound != nil {
			bound = append(bound, carried{p.name, slots[p.slot-1].bound.cname})
		}
	}
	if len(bound) == 0 {
		env["VL_COOKIE"] = true
		return []part{{expr: "env.VL_COOKIE"}}
	}
	var ps []part
	for i, c := range bound {
		if i > 0 {
			ps = append(ps, part{lit: "; "})
		}
		ps = append(ps, part{lit: c.name + "="}, part{expr: c.cname})
	}
	if len(bound) < len(pairs) {
		env["VL_COOKIE"] = true
		ps = append(ps, part{lit: "; "}, part{expr: "env.VL_COOKIE"})
	}
	return ps
}

func resolveJSON(v any, slots []slot, env map[string]bool) any {
	switch x := v.(type) {
	case pending:
		s := &slots[x.slot-1]
		if s.bound != nil {
			return jsExpr(s.bound.cname)
		}
		if s.secret {
			n := envName(s.envName)
			env[n] = true
			return jsExpr("env." + n)
		}
		if x.num {
			return json.Number(x.raw)
		}
		return x.raw
	case jObj:
		out := make(jObj, len(x))
		for i, kv := range x {
			out[i] = jKV{kv.key, resolveJSON(kv.val, slots, env)}
		}
		return out
	case jArr:
		out := make(jArr, len(x))
		for i, e := range x {
			out[i] = resolveJSON(e, slots, env)
		}
		return out
	default:
		return v
	}
}
