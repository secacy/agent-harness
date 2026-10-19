package round

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

var ErrInvalid = errors.New("invalid round evidence")

type reader struct {
	ctx     context.Context
	output  string
	sources map[string]Source
	trees   map[string]string
}

// Decode 拒绝未知字段和额外 JSON 值，用于显式请求与纳入清单。
func Decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func digest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}

func contained(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func child(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) || relative == "." {
		return "", errors.New("artifact must be a local relative path")
	}
	path, err := gate.Canonical(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	if !contained(root, path) {
		return "", errors.New("artifact escapes its declared root")
	}
	return path, nil
}

func (r *reader) read(path, expected string) ([]byte, bool, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, false, err
	}
	canonical, err := gate.Canonical(path)
	if err != nil {
		return nil, false, err
	}
	info, statErr := os.Stat(canonical)
	if statErr == nil && (!info.Mode().IsRegular() || info.Size() > 16<<20) {
		return nil, false, errors.New("evidence must be a bounded regular file")
	}
	data, err := os.ReadFile(canonical)
	if errors.Is(err, os.ErrNotExist) {
		if prior, ok := r.sources[canonical]; ok && !prior.Missing {
			return nil, false, fmt.Errorf("%w: source disappeared: %s", ErrInvalid, canonical)
		}
		r.sources[canonical] = Source{Missing: true}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(data) > 16<<20 {
		return nil, false, errors.New("evidence file exceeds size limit")
	}
	hash := gate.Digest(data)
	if prior, ok := r.sources[canonical]; ok && (prior.Missing || prior.SHA256 != hash) {
		return nil, false, fmt.Errorf("%w: source changed between reads: %s", ErrInvalid, canonical)
	}
	if expected != "" && hash != expected {
		return nil, false, fmt.Errorf("%w: digest mismatch: %s", ErrInvalid, canonical)
	}
	copy := "inputs/" + hash + ".data"
	if err = gate.Write(filepath.Join(r.output, copy), data); err != nil {
		return nil, false, err
	}
	r.sources[canonical] = Source{SHA256: hash, Copy: copy}
	return data, true, nil
}

func (r *reader) json(path, hash string, out any) (bool, error) {
	raw, exists, err := r.read(path, hash)
	if err != nil || !exists {
		return exists, err
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("%w: invalid JSON: %s", ErrInvalid, path)
	}
	return true, nil
}

func (r *reader) tree(path, expected string) error {
	files, err := gate.ReadFiles(path)
	if err != nil {
		return err
	}
	hash := gate.TreeHash(files)
	if hash != expected {
		return fmt.Errorf("%w: tree digest mismatch: %s", ErrInvalid, path)
	}
	r.trees[path] = hash
	for name, data := range files {
		if _, _, err = r.read(filepath.Join(path, name), gate.Digest(data)); err != nil {
			return err
		}
	}
	return nil
}

func (r *reader) unchanged() error {
	for path, source := range r.sources {
		data, err := os.ReadFile(path)
		if source.Missing && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || source.Missing || gate.Digest(data) != source.SHA256 {
			return fmt.Errorf("%w: source changed: %s", ErrInvalid, path)
		}
	}
	for _, source := range r.sources {
		if source.Missing {
			continue
		}
		copy, err := os.ReadFile(filepath.Join(r.output, source.Copy))
		if err != nil || gate.Digest(copy) != source.SHA256 {
			return fmt.Errorf("%w: archived input changed", ErrInvalid)
		}
	}
	for path, before := range r.trees {
		files, err := gate.ReadFiles(path)
		if err != nil || gate.TreeHash(files) != before {
			return fmt.Errorf("%w: tree changed: %s", ErrInvalid, path)
		}
	}
	return nil
}
