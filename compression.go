package main

import (
	"context"
	"io"
	"os"
	"runtime"

	"github.com/klauspost/compress/zstd"
)

// Reads in bounded chunks, checks cancellation and reports bytes actually read.
// Neither compression nor restore loads the entire archive into memory.
type progressReader struct {
	ctx      context.Context
	r        io.Reader
	done     int64
	progress func(int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	r.done += int64(n)
	if r.progress != nil {
		r.progress(r.done)
	}
	return n, err
}

func compressSnapshot(ctx context.Context, source, dest, format string, progress func(int64)) (result error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); result == nil {
			result = err
		}
	}()
	level := zstd.SpeedFastest
	if format == "zstd-small" {
		level = zstd.SpeedBetterCompression
	}
	encoder, err := zstd.NewWriter(out, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(min(runtime.GOMAXPROCS(0), 4)), zstd.WithEncoderCRC(true))
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(encoder, &progressReader{ctx: ctx, r: in, progress: progress}, make([]byte, 1<<20))
	closeErr := encoder.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return out.Sync()
}
