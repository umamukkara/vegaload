package inputs

import (
	"strings"
	"testing"
)

func TestParse_SlotsAndLiterals(t *testing.T) {
	tpl, err := Parse("id={data.users.id} name={data.users.name}", false)
	if err != nil {
		t.Fatal(err)
	}
	slots := tpl.Slots()
	if len(slots) != 2 || slots[0].Set != "users" || slots[0].Column != "id" || slots[1].Column != "name" {
		t.Fatalf("slots = %+v", slots)
	}
	got, err := tpl.Fill(map[string]map[string]any{
		"users": {"id": "7", "name": "ann"},
	})
	if err != nil || got != "id=7 name=ann" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestParse_LeavesNonPlaceholders(t *testing.T) {
	in := `{"note":"{data. not a placeholder}","id":"{id}","x":"{data.users.}"}`
	tpl, err := Parse(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.HasSlots() {
		t.Fatalf("slots = %+v", tpl.Slots())
	}
	got, err := tpl.Fill(nil)
	if err != nil || got != in {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestParse_SuffixOnlyIntAndFloat(t *testing.T) {
	tpl, err := Parse("{data.users.id:string}", false)
	if err != nil || tpl.HasSlots() {
		t.Fatalf("a foreign suffix is not a placeholder: %+v %v", tpl.Slots(), err)
	}
	tpl, err = Parse("n={data.users.id:int} x={data.users.score:float}", false)
	if err != nil || len(tpl.Slots()) != 2 {
		t.Fatal(err)
	}
	got, err := tpl.Fill(map[string]map[string]any{"users": {"id": "42", "score": "1.5"}})
	if err != nil || got != "n=42 x=1.5" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := tpl.Fill(map[string]map[string]any{"users": {"id": "abc", "score": "1.5"}}); err == nil || strings.Contains(err.Error(), "abc") || !strings.Contains(err.Error(), "not an integer") {
		t.Fatalf("err = %v, want an integer error that hides the cell", err)
	}
}

func TestFill_ArgsJSONAndSuffixes(t *testing.T) {
	tpl, err := Parse(`[{data.users.id},{data.users.id:int},{data.users.ok},{data.users.note}]`, true)
	if err != nil {
		t.Fatal(err)
	}
	csv, err := tpl.Fill(map[string]map[string]any{"users": {"id": "42", "ok": "true", "note": `a"b`}})
	if err != nil {
		t.Fatal(err)
	}
	if csv != `["42",42,"true","a\"b"]` {
		t.Fatalf("csv args = %s", csv)
	}
	js, err := tpl.Fill(map[string]map[string]any{"users": {"id": float64(42), "ok": true, "note": "ann"}})
	if err != nil {
		t.Fatal(err)
	}
	if js != `[42,42,true,"ann"]` {
		t.Fatalf("json args = %s", js)
	}
}

func TestFill_TextOutsideArgs(t *testing.T) {
	tpl, err := Parse("session:{data.keys.token}", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tpl.Fill(map[string]map[string]any{"keys": {"token": "a b"}})
	if err != nil || got != "session:a b" {
		t.Fatalf("got %q %v", got, err)
	}
	num, err := Parse("{data.users.id}", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err = num.Fill(map[string]map[string]any{"users": {"id": float64(42)}})
	if err != nil || got != "42" {
		t.Fatalf("plain number = %q %v", got, err)
	}
}

func TestFill_SameRowForOneSet(t *testing.T) {
	tpl, err := Parse("{data.users.id}-{data.users.id}", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tpl.Fill(map[string]map[string]any{"users": {"id": "9"}})
	if err != nil || got != "9-9" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestFill_MissingColumn(t *testing.T) {
	tpl, _ := Parse("{data.users.missing}", false)
	_, err := tpl.Fill(map[string]map[string]any{"users": {"id": "1"}})
	if err == nil || !strings.Contains(err.Error(), "missing") || strings.Contains(err.Error(), "1") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckColumns(t *testing.T) {
	set, err := LoadSet("users", write(t, "users.csv", "id,name\n1,ann\n"))
	if err != nil {
		t.Fatal(err)
	}
	sets := map[string]*Set{"users": set}
	tpl, _ := Parse("{data.users.id}", false)
	if err := CheckColumns(tpl.Slots(), sets); err != nil {
		t.Fatal(err)
	}
	bad, _ := Parse("{data.users.city}", false)
	if err := CheckColumns(bad.Slots(), sets); err == nil || !strings.Contains(err.Error(), "users.csv") || !strings.Contains(err.Error(), "city") {
		t.Fatalf("err = %v", err)
	}
	other, _ := Parse("{data.files.name}", false)
	if err := CheckColumns(other.Slots(), sets); err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("err = %v", err)
	}
}

func TestFill_NoSlotsAllocatesNothing(t *testing.T) {
	tpl, err := Parse("select 1", false)
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		got, err := tpl.Fill(nil)
		if err != nil || got != "select 1" {
			t.Fatalf("got %q %v", got, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("fill with no slots allocated %v times per call", allocs)
	}
}

func BenchmarkFill_NoSlots(b *testing.B) {
	tpl, err := Parse("select 1", false)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := tpl.Fill(nil); err != nil {
			b.Fatal(err)
		}
	}
}
