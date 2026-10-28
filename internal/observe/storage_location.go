package observe

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"getarm.org/internal/model"
)

// StorageLocation resolves an existing path; optional hardware reports never
// invent capacity. All callers retain the original filesystem identity.
func StorageLocation(ctx context.Context, path string) (model.StorageLocation, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return model.StorageLocation{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return model.StorageLocation{}, err
	}
	r := model.StorageLocation{Path: absolute, At: time.Now().UTC()}
	err = storageLocation(ctx, &r)
	if err == nil && r.Volume == "" {
		err = fmt.Errorf("filesystem identity unavailable")
	}
	return r, err
}
