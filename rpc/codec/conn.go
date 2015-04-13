// Copyright 2013 <chaishushan{AT}gmail.com>. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codec

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"

	wire "github.com/cockroachdb/cockroach/rpc/codec/wire.pb"
	"github.com/gogo/protobuf/proto"
)

const enableSnappy = true

type decompressFunc func(src []byte, m proto.Message) error

var decompressors = [...]decompressFunc{
	wire.CompressionType_NONE:   proto.Unmarshal,
	wire.CompressionType_SNAPPY: snappyDecode,
}

type baseConn struct {
	w        *bufio.Writer
	r        *bufio.Reader
	c        io.Closer
	frameBuf [binary.MaxVarintLen64]byte
}

// Close closes the underlying connection.
func (c *baseConn) Close() error {
	return c.c.Close()
}

func (c *baseConn) sendFrame(data []byte) error {
	// Allocate enough space for the biggest uvarint
	size := c.frameBuf[:]

	if data == nil || len(data) == 0 {
		n := binary.PutUvarint(size, uint64(0))
		return c.write(c.w, size[:n])
	}

	// Write the size and data
	n := binary.PutUvarint(size, uint64(len(data)))
	if err := c.write(c.w, size[:n]); err != nil {
		return err
	}
	return c.write(c.w, data)
}

func (c *baseConn) write(w io.Writer, data []byte) error {
	for index := 0; index < len(data); {
		n, err := w.Write(data[index:])
		if err != nil {
			if nerr, ok := err.(net.Error); !ok || !nerr.Temporary() {
				return err
			}
		}
		index += n
	}
	return nil
}

func (c *baseConn) recvProto(m proto.Message, decompressor decompressFunc) error {
	size, err := binary.ReadUvarint(c.r)
	if err != nil {
		return err
	}
	if size == 0 {
		return nil
	}
	if c.r.Buffered() >= int(size) {
		// Parse proto directly from the buffered data.
		data, err := c.r.Peek(int(size))
		if err != nil {
			return err
		}
		if err := decompressor(data, m); err != nil {
			return err
		}
		// TODO(pmattis): This is a hack to advance the bufio pointer by
		// reading into the same slice that bufio.Reader.Peek
		// returned. In Go 1.5 we'll be able to use
		// bufio.Reader.Discard.
		_, err = io.ReadFull(c.r, data)
		return err
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(c.r, data); err != nil {
		return err
	}
	return decompressor(data, m)
}
