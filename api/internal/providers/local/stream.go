package local

import (
	"context"
	"io"
)

// ctxReader makes a long copy cancellable: every Read first checks the context.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
