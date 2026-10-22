package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/yrashk/arm/internal/model"
)

func OperationError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	var detail *model.Error
	if errors.As(err, &detail) {
		return err
	}
	code := "database_unavailable"
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		code = "database_timeout"
	}
	if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
		code = "database_interrupted"
	}
	message := fmt.Sprintf("database %s did not complete: %v; no source compaction or repair was attempted", operation, err)
	if operation == "backup" {
		message += ". Inspect the destination before retrying; an existing file will never be overwritten"
	}
	return model.Err(code, message)
}
