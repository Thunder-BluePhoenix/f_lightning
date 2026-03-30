package canal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// BinlogPosition stores the last successfully processed binlog position.
type BinlogPosition struct {
	Site string `json:"site"`
	File string `json:"file"`
	Pos  uint32 `json:"pos"`
	GTID string `json:"gtid,omitempty"`
}

func positionFilePath(site string) string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".lightning")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, fmt.Sprintf("binlog_pos_%s.json", sanitize(site)))
}

// SavePosition persists the current binlog position to disk.
func SavePosition(pos BinlogPosition) error {
	data, err := json.MarshalIndent(pos, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(positionFilePath(pos.Site), data, 0644)
}

// LoadPosition reads the saved binlog position from disk.
// Returns an error (os.ErrNotExist) if no position has been saved yet.
func LoadPosition(site string) (*BinlogPosition, error) {
	path := positionFilePath(site)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err // Caller checks os.IsNotExist
	}
	var pos BinlogPosition
	if err := json.Unmarshal(data, &pos); err != nil {
		return nil, fmt.Errorf("position file corrupted: %w", err)
	}
	return &pos, nil
}

// DeletePosition removes the saved position file (use before a full reindex).
func DeletePosition(site string) error {
	return os.Remove(positionFilePath(site))
}

// sanitize replaces characters that are invalid in file names.
func sanitize(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == '/' || c == ':' || c == ' ' {
			out[i] = '_'
		} else {
			out[i] = c
		}
	}
	return string(out)
}
