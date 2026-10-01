// Command sample-app is a tiny, deliberately imperfect HTTP service for
// VegaLoad's own getting-started walkthrough (see ../README.md): there
// is nothing here VegaLoad depends on, it's just something worth load
// testing. It keeps an in-memory list of "widgets" and exposes:
//
//	GET  /health        -- always 200, near-zero latency
//	GET  /widgets        -- list widgets, 10-40ms simulated latency
//	GET  /widgets/{id}   -- one widget, or 404
//	POST /widgets        -- create a widget, 20-60ms latency, and a
//	                         ~3% random 500 to give a baseline run some
//	                         real failures for `vegaload diagnose` to
//	                         explain
//
// Stdlib only, no third-party dependencies, its own go.mod separate
// from VegaLoad's own module -- it's a target to test, not part of the
// tool.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Widget is the sample app's one resource.
type Widget struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type store struct {
	mu      sync.Mutex
	widgets []Widget
	nextID  int
}

func newStore() *store {
	return &store{
		widgets: []Widget{{ID: 1, Name: "sprocket"}, {ID: 2, Name: "gear"}, {ID: 3, Name: "cam"}},
		nextID:  4,
	}
}

func (s *store) list() []Widget {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Widget, len(s.widgets))
	copy(out, s.widgets)
	return out
}

func (s *store) get(id int) (Widget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.widgets {
		if w.ID == id {
			return w, true
		}
	}
	return Widget{}, false
}

func (s *store) create(name string) Widget {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := Widget{ID: s.nextID, Name: name}
	s.nextID++
	s.widgets = append(s.widgets, w)
	return w
}

// simulateLatency sleeps for a random duration in [minMS, maxMS) --
// real services are never instant, and a flat 0ms response makes for a
// boring first load test report.
func simulateLatency(minMS, maxMS int) {
	time.Sleep(time.Duration(minMS+rand.Intn(maxMS-minMS)) * time.Millisecond)
}

func newMux(s *store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
	})

	mux.HandleFunc("GET /widgets", func(w http.ResponseWriter, r *http.Request) {
		simulateLatency(10, 40)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.list()) //nolint:errcheck
	})

	mux.HandleFunc("GET /widgets/{id}", func(w http.ResponseWriter, r *http.Request) {
		simulateLatency(10, 40)
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		widget, ok := s.get(id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(widget) //nolint:errcheck
	})

	mux.HandleFunc("POST /widgets", func(w http.ResponseWriter, r *http.Request) {
		simulateLatency(20, 60)
		// A ~3% random failure rate, so a baseline run has a few real
		// failures in it -- enough for `vegaload diagnose` to have
		// something to say, without drowning out the passing majority.
		if rand.Intn(100) < 3 {
			http.Error(w, "simulated transient failure", http.StatusInternalServerError)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			http.Error(w, "expected a JSON body with a non-empty \"name\"", http.StatusBadRequest)
			return
		}
		created := s.create(body.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created) //nolint:errcheck
	})

	return mux
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	flag.Parse()

	mux := newMux(newStore())
	log.Printf("sample-app listening on http://%s (GET /health, GET /widgets, GET /widgets/{id}, POST /widgets)", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		fmt.Println(err)
	}
}
