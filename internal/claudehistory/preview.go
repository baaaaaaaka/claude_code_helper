package claudehistory

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
)

const (
	previewBlockSize = 64 * 1024
	previewMaxBytes  = 2 * 1024 * 1024
)

// ReadSessionPreview returns recent messages and whether the byte limit may
// have omitted messages. Full history callers use ReadSessionMessages.
func ReadSessionPreview(ctx context.Context, path string, count int) ([]Message, bool, error) {
	if count <= 0 {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("session is not a regular file: %s", path)
	}
	if info.Size() == 0 {
		return nil, false, nil
	}

	// Grow towards the start of the file. Parse each complete record only
	// once, even when a record spans multiple blocks.
	size := info.Size()
	limit := min(size, previewMaxBytes)
	buf := make([]byte, min(limit, previewBlockSize))
	start, end := len(buf), len(buf)
	var messages, fallback []Message
	for offset := size; offset > size-limit; {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		n := int(min(previewBlockSize, offset-(size-limit)))
		if start < n {
			remainder := end - start
			buf = slices.Grow(buf[:remainder], n)[:remainder+n]
			copy(buf[n:], buf[start:end])
			start, end = n, n+remainder
		}
		start -= n
		offset -= int64(n)
		if _, err := f.ReadAt(buf[start:start+n], offset); err != nil {
			return nil, false, err
		}

		// Only the new block can contain an unseen delimiter. end can
		// include the partial record carried over from the previous block.
		searchEnd := start + n
		for searchEnd > start {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			i := bytes.LastIndexByte(buf[start:searchEnd], '\n')
			if i < 0 {
				break
			}
			i += start
			messages, fallback = addPreviewLine(buf[i+1:end], count, messages, fallback)
			if len(messages) == count {
				slices.Reverse(messages)
				return messages, false, nil
			}
			end, searchEnd = i, i
		}
		if offset == 0 {
			messages, fallback = addPreviewLine(buf[start:end], count, messages, fallback)
		}
	}
	if len(messages) == 0 {
		messages = fallback
	}
	slices.Reverse(messages)
	return messages, size > limit, nil
}

func addPreviewLine(line []byte, count int, messages, fallback []Message) ([]Message, []Message) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return messages, fallback
	}
	if msg, ok := parseLineMessage(line); ok {
		return append(messages, msg), nil
	}
	if len(messages) == 0 && len(fallback) < count {
		items := parseLineFallbackMessages(line)
		slices.Reverse(items)
		fallback = append(fallback, items[:min(len(items), count-len(fallback))]...)
	}
	return messages, fallback
}
