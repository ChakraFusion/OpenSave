package p2p

import (
	"net/http"
	"sync"
)

// A peer's reports that carry state ("sync-complete", "in-sync") are answered
// before what they record is done, and are sent again until they are answered
// (ReportSyncEvent). So one can arrive twice — the first time it arrived, its
// answer was what got lost — and two can be recorded at once. reportsRecorded
// does each report's recording once, and one at a time.

// reportsRemembered is how many report IDs are kept to recognise a report
// sent again. A report is sent again within a minute, among a handful.
const reportsRemembered = 256

type reportsRecorded struct {
	mu   sync.Mutex // held while a report is recorded
	seen map[string]bool
	ids  []string // in arrival order, to forget the oldest
}

// once runs record for a report unless a report with the same ID already ran.
// A report without an ID, from an older build, is never sent again, and always
// runs.
func (r *reportsRecorded) once(data map[string]any, record func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, _ := data["reportId"].(string); id != "" {
		if r.seen[id] {
			return
		}
		if r.seen == nil {
			r.seen = map[string]bool{}
		}
		r.seen[id] = true
		r.ids = append(r.ids, id)
		if len(r.ids) > reportsRemembered {
			delete(r.seen, r.ids[0])
			r.ids = r.ids[1:]
		}
	}
	record()
}

// answerNow sends the report's answer at once, so the sender is done with it
// while the handler goes on to record it.
func answerNow(w http.ResponseWriter) {
	jsonOK(w, map[string]any{"success": true})
	_ = http.NewResponseController(w).Flush()
}
