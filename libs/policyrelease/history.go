package policyrelease

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// HistoryEntry is the outcome of one RunOnce pass, recorded regardless of
// whether it activated - the Admin Console's revision and activation module
// (issue #22) needs a release that failed to activate visibly distinguished
// from one that succeeded, and a failed attempt produces no Archive to read
// that back from.
type HistoryEntry struct {
	Revision   string    `json:"revision"`
	Commit     string    `json:"commit"`
	Activated  bool      `json:"activated"`
	Error      string    `json:"error,omitempty"`
	RecordedAt time.Time `json:"recordedAt"`
}

const historyFile = "history.jsonl"

// RecordAttempt appends one HistoryEntry to the store's history log. It is
// append-only: history.jsonl is never rewritten, so no attempt already
// recorded can be edited or dropped by a later one.
func (s *Store) RecordAttempt(entry HistoryEntry) error {
	if entry.RecordedAt.IsZero() {
		entry.RecordedAt = time.Now().UTC()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("policyrelease: encoding history entry: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("policyrelease: creating store directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(s.dir, historyFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("policyrelease: opening history log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("policyrelease: writing history entry: %w", err)
	}
	return nil
}

// History returns every recorded attempt, oldest first.
func (s *Store) History() ([]HistoryEntry, error) {
	f, err := os.Open(filepath.Join(s.dir, historyFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("policyrelease: opening history log: %w", err)
	}
	defer f.Close()

	var entries []HistoryEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry HistoryEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("policyrelease: parsing history entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("policyrelease: reading history log: %w", err)
	}
	return entries, nil
}
