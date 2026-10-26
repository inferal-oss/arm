package receiver

import (
	"fmt"
	"io"
)

const maxLiteralToken = 256 << 10

// rsync/token.c:recvToken
func (rt *Transfer) recvToken() (token int32, data []byte, _ error) {
	var err error
	token, err = rt.Conn.ReadInt32()
	if err != nil {
		return 0, nil, err
	}
	if token <= 0 {
		return token, nil, nil
	}
	if token > maxLiteralToken {
		return 0, nil, fmt.Errorf("literal token %d exceeds limit %d", token, maxLiteralToken)
	}
	data = make([]byte, int(token))
	if _, err := io.ReadFull(rt.Conn.Reader, data); err != nil {
		return 0, nil, err
	}
	return token, data, nil
}
