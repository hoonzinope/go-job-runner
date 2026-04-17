package logwriter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type Reader struct{}

func NewReader() *Reader {
	return &Reader{}
}

func (r *Reader) ReadContent(path string, offset, limit, tail int64) (string, int64, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, 0, err
	}

	start := offset
	if tail > 0 {
		if tail > info.Size() {
			tail = info.Size()
		}
		start = info.Size() - tail
	}
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", 0, 0, err
	}

	var data []byte
	if limit > 0 {
		data = make([]byte, limit)
		n, err := io.ReadFull(f, data)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", 0, 0, err
		}
		data = data[:n]
	} else {
		data, err = io.ReadAll(f)
		if err != nil {
			return "", 0, 0, err
		}
	}

	return string(data), start, len(data), nil
}

func (r *Reader) ReadJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(dst); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}
