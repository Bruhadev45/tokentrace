package record

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// DefaultFollowInterval is how often Follow polls the records file.
const DefaultFollowInterval = 500 * time.Millisecond

// Follow tails the records file, invoking fn for every record appended
// after the call. It polls every interval and returns nil when ctx is done.
// A file that does not exist yet is waited for and then read from the start.
func Follow(ctx context.Context, path string, interval time.Duration, fn func(Record)) error {
	if fn == nil {
		return errors.New("follow: nil callback")
	}
	if interval <= 0 {
		interval = DefaultFollowInterval
	}

	// Skip whatever already exists: tail shows new records only.
	var offset int64
	if fi, err := os.Stat(path); err == nil {
		offset = fi.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	var partial []byte
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		next, err := followOnce(path, offset, &partial, fn)
		switch {
		case err == nil:
			offset = next
		case errors.Is(err, os.ErrNotExist):
			// Not created yet (or removed): start from the beginning
			// once it appears.
			offset, partial = 0, nil
		default:
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// followOnce reads any bytes appended past offset, invokes fn per complete
// line, and returns the new offset. Incomplete trailing lines are kept in
// partial until the rest arrives.
func followOnce(path string, offset int64, partial *[]byte, fn func(Record)) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return offset, fmt.Errorf("stat %s: %w", path, err)
	}
	size := fi.Size()
	if size < offset {
		// Truncated or rotated: start over.
		offset, *partial = 0, nil
	}
	if size == offset {
		return offset, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return offset, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, fmt.Errorf("seek %s: %w", path, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, size-offset))
	if err != nil {
		return offset, fmt.Errorf("read %s: %w", path, err)
	}

	buf := append(append([]byte(nil), *partial...), data...)
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		line := buf[:i]
		buf = buf[i+1:]
		var r Record
		if json.Unmarshal(line, &r) == nil {
			fn(r)
		}
	}
	*partial = append([]byte(nil), buf...)
	return offset + int64(len(data)), nil
}
