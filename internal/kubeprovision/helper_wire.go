package kubeprovision

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

func writeBoundedHelperJSON(w io.Writer, value any, limit int) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > limit {
		return errors.New("Kubernetes helper message exceeds bound")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
	if err := writeHelperBytes(w, size[:]); err != nil {
		return err
	}
	return writeHelperBytes(w, raw)
}

func readBoundedHelperJSON(r io.Reader, value any, limit int) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > uint32(limit) {
		return errors.New("Kubernetes helper message exceeds bound")
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return err
	}
	return jsonUnmarshalStrict(raw, value)
}
