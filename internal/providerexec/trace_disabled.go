//go:build !arm_devtrace

package providerexec

type observer struct{}

func (observer) emit(string, string, uint64)             {}
func (observer) sql(string, string, []byte, []byte, int) {}
func (observer) raw(string, string, []byte)              {}
