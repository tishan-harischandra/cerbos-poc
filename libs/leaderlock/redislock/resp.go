package redislock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// This file is a very small RESP client: the eight commands this adapter
// sends, and nothing else.
//
// A Redis client library would be the obvious choice for an application that
// used Redis. This adapter uses it as a lock and sends SET, GET, WATCH,
// MULTI, PEXPIRE, DEL, EXEC and UNWATCH - so a full client would be a large
// dependency, and a connection pool actively unhelpful: WATCH is scoped to a
// connection, and a pooled command could run the compare on one connection
// and the swap on another. The same reasoning already keeps client-go out of
// the K8S_LEASE adapter.

// dialTimeout bounds establishing a connection.
const dialTimeout = 5 * time.Second

// ErrNil is the empty reply: no such key, or a transaction that was aborted
// because a watched key changed. Both are ordinary answers here.
var ErrNil = errors.New("redislock: redis returned no value")

// conn is one Redis connection. Redis serialises commands per connection, and
// this adapter holds exactly one, which is what makes WATCH mean anything.
type conn struct {
	socket net.Conn
	reader *bufio.Reader
}

func dial(ctx context.Context, address, username, password string) (*conn, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	socket, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("redislock: dialling %s: %w", address, err)
	}
	c := &conn{socket: socket, reader: bufio.NewReader(socket)}

	if password != "" {
		args := []string{"AUTH", password}
		if username != "" {
			args = []string{"AUTH", username, password}
		}
		if _, err := c.do(ctx, args...); err != nil {
			_ = c.close()
			return nil, fmt.Errorf("redislock: authenticating: %w", err)
		}
	}
	return c, nil
}

func (c *conn) close() error { return c.socket.Close() }

// do sends one command and reads its reply.
func (c *conn) do(ctx context.Context, args ...string) (any, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dialTimeout)
	}
	if err := c.socket.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("redislock: setting the deadline: %w", err)
	}

	var request strings.Builder
	fmt.Fprintf(&request, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&request, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if _, err := c.socket.Write([]byte(request.String())); err != nil {
		return nil, fmt.Errorf("redislock: sending %s: %w", args[0], err)
	}
	return c.readReply()
}

func (c *conn) readReply() (any, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("redislock: reading the reply: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, errors.New("redislock: empty reply")
	}

	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, fmt.Errorf("redislock: redis reported %s", line[1:])
	case ':':
		value, err := strconv.ParseInt(line[1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("redislock: unreadable integer reply %q", line)
		}
		return value, nil
	case '$':
		length, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("redislock: unreadable bulk length %q", line)
		}
		if length < 0 {
			return nil, ErrNil
		}
		payload := make([]byte, length+2) // the value and its trailing CRLF
		if _, err := readFull(c.reader, payload); err != nil {
			return nil, fmt.Errorf("redislock: reading a bulk reply: %w", err)
		}
		return string(payload[:length]), nil
	case '*':
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("redislock: unreadable array length %q", line)
		}
		if count < 0 {
			// A nil array is EXEC's way of saying the transaction
			// was abandoned because a watched key changed - which
			// is exactly the "somebody else got there first" this
			// adapter is looking for.
			return nil, ErrNil
		}
		items := make([]any, 0, count)
		for range count {
			item, err := c.readReply()
			if err != nil && !errors.Is(err, ErrNil) {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("redislock: unrecognised reply %q", line)
	}
}

func readFull(reader *bufio.Reader, into []byte) (int, error) {
	read := 0
	for read < len(into) {
		n, err := reader.Read(into[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}
