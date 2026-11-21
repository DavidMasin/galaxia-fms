// The event journal through the API: which days there are, and one day's events as a file.

package web

import (
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"net/http"
	"os"
)

// apiJournalHandler lists the days the journal has a file for, newest first.
func (web *Web) apiJournalHandler(w http.ResponseWriter, r *http.Request) {
	// The journal has a lock of its own and writes to disk here; the arena is not kept waiting for that.
	w = streamResponse(w)
	// So that today's size counts what was written in the last second.
	web.arena.Journal.Flush()
	days, err := web.arena.Journal.Days()
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJson(w, http.StatusOK, map[string]any{"days": days, "keepDays": bus.DefaultJournalDays})
}

// apiJournalDayHandler serves the events of one day, one JSON object per line, as they were written.
func (web *Web) apiJournalDayHandler(w http.ResponseWriter, r *http.Request) {
	// A day's file can be large, and nothing here needs the arena: it is read and sent without the arena's lock.
	w = streamResponse(w)
	day := r.PathValue("day")
	path, err := web.arena.Journal.Path(day)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Today's file is still being written; give the reader everything up to now.
	web.arena.Journal.Flush()
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJsonError(w, http.StatusNotFound, fmt.Sprintf("the journal has nothing for %s", day))
			return
		}
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="galaxia-events-%s.jsonl"`, day))
	http.ServeContent(w, r, "", info.ModTime(), file)
}
