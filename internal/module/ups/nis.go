package ups

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Limits of one NIS exchange (spec 012 FR-009, FR-019). apcupsd's lines are
// shorter than 256 bytes and a status reply has fewer than a hundred of them,
// so these only stop a misbehaving server.
const (
	exchangeTimeout = 5 * time.Second
	maxFrame        = 1024
	maxLines        = 512
	maxReply        = 64 << 10
)

// Dialer opens the connection to apcupsd's Network Information Server.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// readStatus performs one NIS exchange (spec 012 FR-009): it connects, sends
// the `status` command and reads the reply's lines until the zero-length
// frame that ends it. Every message is a 2-byte big-endian length followed by
// that many bytes (apcupsd 3.14 src/lib/apclibnis.c). The exchange is bounded
// by the context's deadline and by exchangeTimeout, and the connection is
// closed before it returns, on every path.
func readStatus(ctx context.Context, dial Dialer, address string) ([]string, error) {
	deadline := time.Now().Add(exchangeTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	conn, err := dial(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	// A cancelled collection must not wait for the deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	if _, err := conn.Write(frame("status")); err != nil {
		return nil, fmt.Errorf("send status request: %w", err)
	}
	var lines []string
	total := 0
	head := make([]byte, 2)
	for {
		if _, err := io.ReadFull(conn, head); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, errors.New("apcupsd closed the connection before the end of its reply")
			}
			return nil, wrapCtx(ctx, err)
		}
		n := int(binary.BigEndian.Uint16(head))
		if n == 0 {
			return lines, nil
		}
		if n > maxFrame {
			return nil, fmt.Errorf("reply frame of %d bytes exceeds %d", n, maxFrame)
		}
		total += n
		if total > maxReply || len(lines) >= maxLines {
			return nil, fmt.Errorf("reply exceeds %d lines or %d bytes", maxLines, maxReply)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, errors.New("apcupsd closed the connection in the middle of a frame")
			}
			return nil, wrapCtx(ctx, err)
		}
		lines = append(lines, strings.TrimRight(string(buf), "\r\n\x00"))
	}
}

// frame encodes one NIS message.
func frame(s string) []byte {
	out := make([]byte, 2+len(s))
	binary.BigEndian.PutUint16(out, uint16(len(s))) //nolint:gosec // only the constant "status" is ever framed
	copy(out[2:], s)
	return out
}

// wrapCtx prefers the context's reason (deadline, cancellation) over the
// network error it caused.
func wrapCtx(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("%w: %w", cerr, err)
	}
	return err
}
