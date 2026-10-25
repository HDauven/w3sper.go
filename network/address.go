// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
)

type AddressSyncer struct {
	network *Client
	driver  driver.Protocol
	workers []driver.Protocol
}

// Additional workers must be independent protocol runtimes owned by the caller.
// Batches are split across them with bounded concurrency and no idle goroutines.
func NewAddressSyncer(c *Client, d driver.Protocol, workers ...driver.Protocol) *AddressSyncer {
	return &AddressSyncer{network: c, driver: d, workers: append([]driver.Protocol{d}, workers...)}
}
func le64(n uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, n); return b }
func (s *AddressSyncer) Root(ctx context.Context) ([]byte, error) {
	b, e := s.network.Contracts().WithID(TransferContract).Call(ctx, "root", nil)
	if e == nil && len(b) != 32 {
		return nil, fmt.Errorf("network: root must be 32 bytes")
	}
	return b, e
}
func (s *AddressSyncer) Openings(ctx context.Context, notes []driver.Note) ([][]byte, error) {
	ctx, cancel := s.network.ReadContext(ctx)
	defer cancel()
	marks, e := s.driver.Bookmarks(ctx, notes)
	if e != nil {
		return nil, e
	}
	out := make([][]byte, len(marks))
	for i, mark := range marks {
		b, e := s.network.Contracts().WithID(TransferContract).Call(ctx, "opening", le64(mark))
		if e != nil {
			return nil, e
		}
		if len(b) < 1 || b[0] != 1 {
			return nil, fmt.Errorf("network: missing opening at position %d", mark)
		}
		out[i] = b
	}
	return out, nil
}
func (s *AddressSyncer) Spent(ctx context.Context, nullifiers [][]byte) ([][]byte, error) {
	ctx, cancel := s.network.ReadContext(ctx)
	defer cancel()
	owned := make([][]byte, len(nullifiers))
	for i, n := range nullifiers {
		if len(n) != 32 {
			return nil, fmt.Errorf("network: nullifier must be 32 bytes")
		}
		owned[i] = append([]byte(nil), n...)
	}
	var out [][]byte
	const batchSize = (60*1024 - 8) / 32
	for start := 0; start < len(owned); start += batchSize {
		end := min(start+batchSize, len(owned))
		count := end - start
		b := make([]byte, count*32+8)
		for i, n := range owned[start:end] {
			copy(b[i*32:], n)
		}
		binary.LittleEndian.PutUint32(b[len(b)-8:], uint32(0)-uint32(count*32))
		binary.LittleEndian.PutUint32(b[len(b)-4:], uint32(count))
		raw, e := s.network.Contracts().WithID(TransferContract).Call(ctx, "existing_nullifiers", b)
		if e != nil {
			return nil, e
		}
		if len(raw) < 8 {
			return nil, fmt.Errorf("network: truncated nullifier archive")
		}
		at := len(raw) - 8
		offset := int64(int32(binary.LittleEndian.Uint32(raw[at:])))
		n := int64(binary.LittleEndian.Uint32(raw[at+4:]))
		pos := int64(at) + offset
		if pos < 0 || pos > int64(at) || n > (int64(at)-pos)/32 {
			return nil, fmt.Errorf("network: malformed nullifier archive")
		}
		for i := int64(0); i < n; i++ {
			out = append(out, append([]byte(nil), raw[pos+i*32:pos+(i+1)*32]...))
		}
	}
	return out, nil
}

type SyncOptions struct {
	FromHeight   uint64
	FromPosition *uint64
	ChunkSize    int
	Progress     func(SyncProgress)
}
type SyncProgress struct {
	OwnedCount    int
	Progress      float64
	Current, Last driver.SyncInfo
}
type NoteBatch struct {
	Owned [][]driver.Note
	Info  driver.SyncInfo
}
type NoteStream struct {
	gate                chan struct{}
	once                sync.Once
	body                io.ReadCloser
	ctx                 context.Context
	cancel              context.CancelFunc
	cleanup             []context.CancelFunc
	signers             []*profile.Signer
	workers             []driver.Protocol
	itemSize, chunkSize int
	last                driver.SyncInfo
	progress            func(SyncProgress)
	eof                 bool
}

func (s *AddressSyncer) Notes(ctx context.Context, owners []*profile.Signer, o SyncOptions) (*NoteStream, error) {
	if len(owners) == 0 || len(owners) > 256 {
		return nil, fmt.Errorf("network: expected 1..256 signers")
	}
	if s.driver == nil {
		return nil, fmt.Errorf("network: protocol driver required")
	}
	owners = append([]*profile.Signer(nil), owners...)
	if o.FromPosition != nil {
		position := *o.FromPosition
		o.FromPosition = &position
	}
	ctx, cancel := s.network.ReadContext(ctx)
	stream := &NoteStream{gate: make(chan struct{}, 1), ctx: ctx, cancel: cancel, signers: owners, workers: append([]driver.Protocol(nil), s.workers...), itemSize: int(s.driver.NoteItemSize()), chunkSize: o.ChunkSize, progress: o.Progress}
	stream.gate <- struct{}{}
	fail := func(e error) (*NoteStream, error) { stream.Close(); return nil, e }
	if stream.chunkSize == 0 {
		stream.chunkSize = 512
	}
	if stream.chunkSize < 1 || stream.chunkSize > 65536 || stream.itemSize < 1 {
		return fail(fmt.Errorf("network: invalid note chunk size"))
	}
	for _, signer := range owners {
		if !owners[0].SameSourceOf(signer) {
			return fail(fmt.Errorf("network: owners must share a source"))
		}
		op, close, e := signer.OperationContext(stream.ctx)
		if e != nil {
			return fail(e)
		}
		stream.ctx = op
		stream.cleanup = append(stream.cleanup, close)
	}
	for _, w := range stream.workers {
		if w == nil || w.NoteItemSize() != s.driver.NoteItemSize() {
			return fail(fmt.Errorf("network: mismatched ownership worker"))
		}
	}
	b, e := s.network.Contracts().WithID(TransferContract).Call(stream.ctx, "num_notes", nil)
	if e != nil {
		return fail(e)
	}
	if len(b) != 8 {
		return fail(fmt.Errorf("network: invalid note count"))
	}
	stream.last.Bookmark = binary.LittleEndian.Uint64(b)
	h, e := NewNode(s.network).BlockHeight(stream.ctx)
	if e != nil {
		return fail(e)
	}
	stream.last.BlockHeight = h.Uint64()
	topic, from := "leaves_from_height", o.FromHeight
	if o.FromPosition != nil {
		topic, from = "leaves_from_pos", *o.FromPosition
	}
	resp, e := s.network.Contracts().WithID(TransferContract).Stream(stream.ctx, topic, le64(from), CallOptions{Feeder: true})
	if e != nil {
		return fail(e)
	}
	stream.body = resp.Body
	return stream, nil
}
func (s *NoteStream) Close() error {
	var e error
	s.once.Do(func() {
		s.cancel()
		for _, c := range s.cleanup {
			c()
		}
		if s.body != nil {
			e = s.body.Close()
		}
	})
	return e
}
func (s *NoteStream) Next(ctx context.Context) (out NoteBatch, err error) {
	select {
	case <-ctx.Done():
		return out, ctx.Err()
	case <-s.gate:
	}
	defer func() { s.gate <- struct{}{} }()
	if s.eof {
		return out, io.EOF
	}
	if e := ctx.Err(); e != nil {
		return out, e
	}
	if e := s.ctx.Err(); e != nil {
		_ = s.Close()
		return out, e
	}
	op, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(ctx, func() { cancel(); _ = s.Close() })
	defer func() { stop(); cancel() }()
	b := make([]byte, s.chunkSize*s.itemSize)
	n, e := io.ReadFull(s.body, b)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		_ = s.Close()
		return out, e
	}
	if n%s.itemSize != 0 {
		_ = s.Close()
		return out, fmt.Errorf("network: truncated feeder note (%d bytes)", n)
	}
	if n == 0 {
		s.eof = true
		_ = s.Close()
		return out, io.EOF
	}
	b = b[:n]
	eof := e != nil
	count := len(s.workers)
	if count > n/s.itemSize {
		count = n / s.itemSize
	}
	results := make([]NoteBatch, count)
	errors := make([]error, count)
	var wg sync.WaitGroup
	totalNotes := n / s.itemSize
	for i := 0; i < count; i++ {
		start, end := totalNotes*i/count, totalNotes*(i+1)/count
		wg.Add(1)
		go func(i int, b []byte) {
			defer wg.Done()
			results[i].Owned, results[i].Info, errors[i] = s.workers[i].MapOwned(op, s.signers, b)
		}(i, b[start*s.itemSize:end*s.itemSize])
	}
	wg.Wait()
	out.Owned = make([][]driver.Note, len(s.signers))
	owned := 0
	for i, r := range results {
		if errors[i] != nil {
			_ = s.Close()
			return NoteBatch{}, errors[i]
		}
		if len(r.Owned) != len(s.signers) {
			_ = s.Close()
			return NoteBatch{}, fmt.Errorf("network: malformed ownership result")
		}
		for j, notes := range r.Owned {
			out.Owned[j] = append(out.Owned[j], notes...)
			owned += len(notes)
		}
		out.Info.BlockHeight = max(out.Info.BlockHeight, r.Info.BlockHeight)
		out.Info.Bookmark = max(out.Info.Bookmark, r.Info.Bookmark)
	}
	if e := op.Err(); e != nil {
		_ = s.Close()
		return NoteBatch{}, e
	}
	if s.progress != nil {
		p := float64(1)
		if s.last.Bookmark > 0 {
			p = min(float64(out.Info.Bookmark+1)/float64(s.last.Bookmark), 1)
		}
		s.progress(SyncProgress{OwnedCount: owned, Progress: p, Current: out.Info, Last: s.last})
	}
	if e := op.Err(); e != nil {
		_ = s.Close()
		return NoteBatch{}, e
	}
	if eof {
		s.eof = true
		_ = s.Close()
	}
	return out, nil
}
