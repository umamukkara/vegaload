package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vegaload/vegaload/internal/doctor"
	"github.com/vegaload/vegaload/internal/scripting/js"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

// protocolRecord is one protocol in `vegaload protocols -output json`.
// Field order is the contract. Add fields later; do not rename or remove one.
type protocolRecord struct {
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Status      string   `json:"status"`
	Since       string   `json:"since"`
	URLSchemes  []string `json:"url_schemes"`
	ScriptCalls []string `json:"script_calls"`
	Modes       []string `json:"modes"`
	Options     []string `json:"options"`
	TLSOptions  bool     `json:"tls_options"`
	WriteFlags  []string `json:"write_flags"`
	Examples    []string `json:"examples"`
}

type protocolsDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Protocols     []protocolRecord `json:"protocols"`
}

const protocolsSchemaVersion = 1

func cmdProtocols(args []string) int {
	fs := flag.NewFlagSet("protocols", flag.ContinueOnError)
	output := fs.String("output", "text", "output mode: text or json")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: vegaload protocols [flags] [NAME]")
		fmt.Fprintln(fs.Output(), "")
		fmt.Fprintln(fs.Output(), "List every supported protocol, or one protocol. NAME is a -protocol value.")
		fmt.Fprintln(fs.Output(), "Example paths are relative to a checkout of this repository.")
		fmt.Fprintln(fs.Output(), "A released binary does not include the examples folder.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(flagsBeforeArgs(args)); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *output != "text" && *output != "json" {
		fmt.Fprintf(os.Stderr, "vegaload protocols: -output %q: want text or json\n", *output)
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintf(os.Stderr, "vegaload protocols: want at most one protocol name, got %d\n", fs.NArg())
		fs.Usage()
		return 2
	}

	var list []driverSpec
	if fs.NArg() == 0 {
		list = drivers
	} else {
		spec, ok := findDriver(fs.Arg(0))
		if !ok {
			fmt.Fprintf(os.Stderr, "vegaload protocols: %s\n", unknownProtocolMessage(fs.Arg(0)))
			return 2
		}
		list = []driverSpec{spec}
	}

	doc := protocolsDocument{SchemaVersion: protocolsSchemaVersion, Protocols: make([]protocolRecord, len(list))}
	for i, d := range list {
		doc.Protocols[i] = d.record()
	}
	if *output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			fmt.Fprintf(os.Stderr, "vegaload protocols: %v\n", err)
			return 1
		}
		return 0
	}
	if len(list) == 1 {
		printProtocolDetail(os.Stdout, list[0])
		return 0
	}
	printProtocolTable(os.Stdout)
	return 0
}

// flagsBeforeArgs moves flags in front of the protocol name, so
// `vegaload protocols sftp -output json` parses the same as the other order.
func flagsBeforeArgs(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if (a == "-output" || a == "--output") && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

func (d driverSpec) record() protocolRecord {
	return protocolRecord{
		Name:        d.name,
		Summary:     d.summary,
		Status:      "supported",
		Since:       d.since,
		URLSchemes:  filled(doctor.SchemesFor(d.name)),
		ScriptCalls: filled(d.scriptCalls()),
		Modes:       filled(d.modes),
		Options:     filled(d.options),
		TLSOptions:  d.tls,
		WriteFlags:  filled(d.writeFlags),
		Examples:    filled(d.examples),
	}
}

func (d driverSpec) scriptCalls() []string {
	global := d.scriptGlobal
	var names []string
	switch global {
	case "http":
		names = js.ScriptHTTPMethods()
	case "ws":
		names = js.ScriptWSMethods()
	default:
		global = d.name
		names = netapi.ProtoFunctions()[d.name]
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, global+"."+name)
	}
	return out
}

func filled(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func exampleCell(d driverSpec) string {
	if len(d.examples) == 0 {
		return "(none yet)"
	}
	name := d.examples[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func printProtocolTable(w io.Writer) {
	type row struct {
		name, since, schemes, example string
		calls                         []string
	}
	rows := make([]row, len(drivers))
	pw, sw, uw, ew := len("PROTOCOL"), len("SINCE"), len("URL SCHEMES"), len("EXAMPLE")
	for i, d := range drivers {
		rec := d.record()
		rows[i] = row{
			name:    d.name,
			since:   d.since,
			schemes: strings.Join(rec.URLSchemes, ", "),
			calls:   rec.ScriptCalls,
			example: exampleCell(d),
		}
		pw = max(pw, len(rows[i].name))
		sw = max(sw, len(rows[i].since))
		uw = max(uw, len(rows[i].schemes))
		ew = max(ew, len(rows[i].example))
	}
	callW := 100 - (pw + sw + uw + ew + 8)
	if callW < len("SCRIPT CALLS") {
		callW = len("SCRIPT CALLS")
	}
	fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %s\n", pw, "PROTOCOL", sw, "SINCE", uw, "URL SCHEMES", callW, "SCRIPT CALLS", "EXAMPLE")
	for _, row := range rows {
		lines := wrapItems(row.calls, callW)
		for i, line := range lines {
			if i == 0 {
				fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %s\n", pw, row.name, sw, row.since, uw, row.schemes, callW, line, row.example)
				continue
			}
			fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", pw, "", sw, "", uw, "", line)
		}
	}
}

func printProtocolDetail(w io.Writer, d driverSpec) {
	rec := d.record()
	fmt.Fprintf(w, "%s (since %s)\n", d.name, d.since)
	fmt.Fprintln(w, d.summary)
	writeLabel(w, "URL schemes:", strings.Join(rec.URLSchemes, ", "))
	writeLabel(w, "-protocol:", d.name)
	writeWrapped(w, "Script calls:", rec.ScriptCalls)
	if len(d.modes) > 0 {
		writeWrapped(w, "Modes:", rec.Modes)
	}
	writeWrapped(w, "Options:", rec.Options)
	writeLabel(w, "TLS options:", tlsText(d))
	writeLabel(w, "Writes:", writesText(d))
	if len(d.examples) == 0 {
		writeLabel(w, "Examples:", "(none yet)")
	} else {
		for i, ex := range d.examples {
			label := "Examples:"
			if i > 0 {
				label = ""
			}
			writeLabel(w, label, ex)
		}
	}
	scheme := ""
	if len(rec.URLSchemes) > 0 {
		scheme = rec.URLSchemes[0]
	}
	writeLabel(w, "Try it:", fmt.Sprintf("vegaload doctor -target %s://HOST (checks the target is reachable)", scheme))
}

func writeLabel(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%-13s %s\n", label, value)
}

func writeWrapped(w io.Writer, label string, items []string) {
	lines := wrapItems(items, 84)
	if len(lines) == 0 {
		writeLabel(w, label, "")
		return
	}
	writeLabel(w, label, lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(w, "%-13s %s\n", "", line)
	}
}

func wrapItems(items []string, width int) []string {
	if len(items) == 0 {
		return []string{""}
	}
	if width < 1 {
		width = 1
	}
	var lines []string
	cur := ""
	for i, item := range items {
		extra := 0
		if i < len(items)-1 {
			extra = 1 // a wrapped line keeps its trailing comma inside the width
		}
		if cur == "" {
			cur = item
			continue
		}
		if len(cur)+2+len(item)+extra > width {
			lines = append(lines, cur+",")
			cur = item
			continue
		}
		cur += ", " + item
	}
	lines = append(lines, cur)
	return lines
}

func tlsText(d driverSpec) string {
	if d.tls {
		return "yes"
	}
	for _, opt := range d.options {
		if opt == "host_key" {
			return "no (SFTP uses SSH host keys: host_key)"
		}
	}
	return "no"
}

func writesText(d driverSpec) string {
	switch {
	case len(d.writeFlags) == 0:
		return "none"
	case len(d.writeFlags) == 1:
		return "off by default. Needs allow_writes=true."
	case d.name == "ftp" || d.name == "sftp":
		return "off by default. Needs allow_writes=true. Delete and fixed upload paths also need allow_admin=true."
	default:
		return "off by default. Needs allow_writes=true. Admin actions also need allow_admin=true."
	}
}

// unknownProtocolMessage is the body of the usage error for a bad
// -protocol value. The caller adds the "vegaload run:" or
// "vegaload protocols:" prefix.
func unknownProtocolMessage(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unknown -protocol %q\n", name)
	b.WriteString("Supported protocols:\n")
	b.WriteString(strings.Join(wrapItems(protocolNames(), 90), "\n"))
	b.WriteString("\nRun `vegaload protocols` for details and examples.")
	if hint := suggestProtocol(name); hint != "" {
		fmt.Fprintf(&b, "\nDid you mean %q?", hint)
	}
	return b.String()
}

// suggestProtocol reports a close -protocol name. It tries a different
// case, then a known URL scheme, then one edit. The first table hit wins.
func suggestProtocol(name string) string {
	for _, n := range protocolNames() {
		if n != name && strings.EqualFold(n, name) {
			return n
		}
	}
	if p := doctor.ProtocolForScheme(name); p != "" && p != name {
		return p
	}
	best := ""
	folded := strings.ToLower(name)
	for _, n := range protocolNames() {
		if editDistance(folded, n) == 1 {
			best = n
			break
		}
	}
	return best
}

func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(a)-len(b) > 1 {
		return len(a) - len(b)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// protocolReadmeTable is the Supported protocols table in README.md.
// A test compares the marked region of the README with this text.
func protocolReadmeTable() string {
	var b strings.Builder
	b.WriteString("| Protocol | Since | URL schemes | Example |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, d := range drivers {
		rec := d.record()
		example := "(none yet)"
		if len(d.examples) > 0 {
			example = fmt.Sprintf("[%s](%s)", exampleCell(d), d.examples[0])
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", d.name, d.since, strings.Join(rec.URLSchemes, ", "), example)
	}
	return b.String()
}
