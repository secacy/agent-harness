//go:build !darwin && !linux

package writer

func acquire(string, bool) (*Lease, error) { return nil, ErrUnsupported }
